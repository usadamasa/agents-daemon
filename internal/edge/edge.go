// Package edge はセッションをまたぐ作業の依存 (DAG の辺、#134) を XDG state 配下に置く。辺の正は作業を渡す側
// (workspace) が `agents-daemon edge add` で書いたもの。読むのは `edge list` で、ボードへの描画はまだ無い。
//
//	edges/<domain>/<id>.json   1 本 1 ファイル
//
// 辺は「before が済まないと after を始められない」を表す。端点は種類付きの参照と、その作業の cwd から求めた
// repo (ボードの行と同じ鍵) を持つ。参照の種類:
//
//	session:<セッション名>          `claude --name` で渡したセッション。渡す時点でタスク ID はまだ無い
//	issue:<owner>/<repo>#<番号>     渡した issue
//	pr:<owner>/<repo>#<番号>        渡した PR
//	task:<session_id>/<task_id>     ボードのタスクのノードそのもの
//
// id は before と after の参照から決めるので、同じ辺の二重登録は同じファイルを書き直すだけになる
// (Idempotent Receiver)。両端の domain が違う辺は書かない (私用と業務を混ぜない、internal/domain)。
package edge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention は更新の無い辺を残す期間。消すのは daemon の housekeeping (Prune) で、mtime で判定する。
// 端点がボードに出うる最も長いタスクのノード (終了したセッションの未完了のタスク、7 日) に合わせる。
// それを過ぎた辺は両端ともボードに出ない。渡す側が登録し直すと数え直す。
const Retention = 7 * 24 * time.Hour

// refPattern は参照の形。issue / pr の repo は GitHub の owner/name、task は session_id と task_id の組。
var refPattern = regexp.MustCompile(`^(?:session:\S+|(?:issue|pr):[\w.-]+/[\w.-]+#[0-9]+|task:[^\s/]+/[^\s/]+)$`)

// Endpoint は辺の端点。書く側は Ref と CWD (その作業のセッションを起こしたディレクトリ) を渡し、Repo は Add が
// CWD の main worktree のパス (git の外なら CWD) で埋める。
type Endpoint struct {
	Ref  string `json:"ref"`
	CWD  string `json:"cwd"`
	Repo string `json:"repo"`
}

// Edge は辺 1 本の記録。
type Edge struct {
	ID        string        `json:"id"`
	Domain    domain.Domain `json:"domain"`
	Before    Endpoint      `json:"before"`
	After     Endpoint      `json:"after"`
	Note      string        `json:"note,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// Added は Add の結果。同じ辺が既にあったかを Created で分ける。
type Added struct {
	Created bool `json:"created"`
	Edge    Edge `json:"edge"`
}

// Removed は Remove の結果。無い辺を消したときは Removed が false になる。
type Removed struct {
	Removed bool   `json:"removed"`
	ID      string `json:"id"`
}

// Store は edges/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "edges")}
}

// Add は辺を書く。同じ辺があれば created_at を残して書き直す (note と repo は後から書いた側)。
func (s Store) Add(before, after Endpoint, note string, classify func(string) domain.Domain, now time.Time) (Added, error) {
	d, id, err := s.resolve(&before, &after, classify)
	if err != nil {
		return Added{}, err
	}
	dir := filepath.Join(s.Dir, string(d))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Added{}, fmt.Errorf("edges の作成に失敗: %w", err)
	}
	before.Repo, after.Repo = gitrepo.RepoKey(before.CWD), gitrepo.RepoKey(after.CWD)
	e := Edge{ID: id, Domain: d, Before: before, After: after, Note: strings.TrimSpace(note), CreatedAt: now}
	existing, found := readEdge(filepath.Join(dir, id+".json"))
	if found {
		e.CreatedAt = existing.CreatedAt
	}
	if err := statefile.WriteJSONAtomic(filepath.Join(dir, id+".json"), ".edge.*", e); err != nil {
		return Added{}, err
	}
	return Added{Created: !found, Edge: e}, nil
}

// Remove は before と after の辺を消す。無い辺は消さずに Removed を false で返す。
func (s Store) Remove(before, after Endpoint, classify func(string) domain.Domain) (Removed, error) {
	d, id, err := s.resolve(&before, &after, classify)
	if err != nil {
		return Removed{}, err
	}
	err = os.Remove(filepath.Join(s.Dir, string(d), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Removed{ID: id}, nil
	}
	if err != nil {
		return Removed{}, err
	}
	return Removed{Removed: true, ID: id}, nil
}

// Find は domain の辺のうち、どちらかの端点が repo に、どちらかの端点が ref に一致するものを登録の順に返す。
// 空の条件は絞らない。該当なしは空の配列。壊れたファイルは読み飛ばす。
func (s Store) Find(d domain.Domain, repo, ref string) ([]Edge, error) {
	edges := []Edge{}
	if s.Dir == "" {
		return edges, nil
	}
	dir := filepath.Join(s.Dir, string(d))
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !isEdgeFile(entry) {
			continue
		}
		e, ok := readEdge(filepath.Join(dir, entry.Name()))
		if !ok {
			continue
		}
		if e.touches(repo, ref) {
			edges = append(edges, e)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if !edges[i].CreatedAt.Equal(edges[j].CreatedAt) {
			return edges[i].CreatedAt.Before(edges[j].CreatedAt)
		}
		return edges[i].ID < edges[j].ID
	})
	return edges, nil
}

// resolve は両端を検証して整え (参照の前後の空白を落とす)、辺の domain と id を返す。
func (s Store) resolve(before, after *Endpoint, classify func(string) domain.Domain) (domain.Domain, string, error) {
	if s.Dir == "" {
		return "", "", errors.New("edges の置き場所が無い")
	}
	for _, ep := range []*Endpoint{before, after} {
		if err := normalizeEndpoint(ep); err != nil {
			return "", "", err
		}
	}
	if before.Ref == after.Ref {
		return "", "", fmt.Errorf("%s から自分自身への辺は書かない", before.Ref)
	}
	d := classify(before.CWD)
	if other := classify(after.CWD); other != d {
		return "", "", fmt.Errorf("%s (%s) と %s (%s) は私用と業務をまたぐので書かない", before.Ref, d, after.Ref, other)
	}
	return d, edgeID(before.Ref, after.Ref), nil
}

// normalizeEndpoint は参照の前後の空白を落とし、参照の形と cwd が絶対パスであることを確かめる。
func normalizeEndpoint(ep *Endpoint) error {
	ep.Ref = strings.TrimSpace(ep.Ref)
	if !refPattern.MatchString(ep.Ref) {
		return fmt.Errorf("参照 %q は session:<名前> / issue:<owner>/<repo>#<番号> / pr:<owner>/<repo>#<番号> / task:<session_id>/<task_id> のどれでもない", ep.Ref)
	}
	if !filepath.IsAbs(ep.CWD) {
		return fmt.Errorf("%s の cwd %q が絶対パスでない", ep.Ref, ep.CWD)
	}
	return nil
}

// edgeID は向きを含めた両端の参照から id を決める。repo と cwd は含めず、同じ参照の組を同じ辺とみなす。
func edgeID(before, after string) string {
	sum := sha256.Sum256([]byte(before + "\x00" + after))
	return hex.EncodeToString(sum[:8])
}

// touches は辺のどちらかの端点が repo に、どちらかの端点が ref に一致するか。空の条件は一致とみなす。
func (e Edge) touches(repo, ref string) bool {
	return (repo == "" || e.Before.Repo == repo || e.After.Repo == repo) && (ref == "" || e.Before.Ref == ref || e.After.Ref == ref)
}

// isEdgeFile は書きかけの一時ファイル (先頭が .) を除いた辺のファイルか。
func isEdgeFile(e os.DirEntry) bool {
	name := e.Name()
	return !e.IsDir() && name[0] != '.' && filepath.Ext(name) == ".json"
}

func readEdge(path string) (Edge, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- edges/ 配下の検証済みのパス
	if err != nil {
		return Edge{}, false
	}
	var e Edge
	if err := json.Unmarshal(data, &e); err != nil || e.ID == "" {
		return Edge{}, false
	}
	return e, true
}
