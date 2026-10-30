// Package edge はセッションをまたぐ作業の依存 (DAG の辺、#134) を XDG state 配下に置く。辺の正は作業を渡す側
// (workspace) が `agents-daemon edge add` で書いたもの。読むのは `edge list` と、依存図の DAG を組む deps パッケージ。
//
//	edges/<id>.json   1 本 1 ファイル
//	edges/.lock       Add、Remove、Bind、Prune を直列にするロック
//
// 辺は「before が済まないと after を始められない」を表す。端点は参照 (ref の Ref のうち todo: 以外) と、
// その作業の cwd から求めた repo (依存図の repo と同じ鍵) を持つ。
//
// session:<名前> の端点は、daemon が台帳 (daemon/ledger) のセッションに 1 回だけ結びつけ (Bind)、
// sid:<session_id> に書き換える。書いたときの参照は bound_from に残し、結びついた端点は二度と結び直さない。
// どのセッションを選ぶかは呼び手の Pick が決める (daemon は ledger.Pick: 辺を書いた時点で動いていた同名のうち
// 最も新しく起きたもの、無ければ後から最初に現れたもの)。取り違えたら `edge rm` してから sid: で書き直す。
//
// id は書いたときの両端の参照 (結びつける前の参照) から決め、結びつけても変わらない。同じ辺の二重登録は同じ
// ファイルを書き直すだけになる (Idempotent Receiver)。
//   - 結びついた後に同じ参照の組で `edge add` すると、結びついた端点を残して note と cwd だけを書き直す
//   - 結びついた後の参照の組 (sid:) で `edge add` / `edge rm` しても、今の参照がその組の辺に当たる
//   - `edge rm` は書いたときの参照の組でも、結びついた後の参照の組でも消せる
//
// Add、Remove、Bind は edges/.lock を取って読んでから書く。ロックが無いと、Add が読んだ後に結びついた端点を
// 名前に戻し、Bind が読んだ後に Remove で消した辺を作り直す (models/handoff の EdgeBind)。
package edge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention は更新の無い辺を残す期間。消すのは daemon の housekeeping (Prune) で、mtime で判定する。
// 端点が依存図に出うる最も長いタスクのノード (終了したセッションの未完了のタスク、7 日) に合わせる。
// それを過ぎた辺は両端とも依存図に出ない。渡す側が登録し直すと数え直す。
const Retention = 7 * 24 * time.Hour

// lockFile は edges/ で Add、Remove、Bind、Prune を直列にするロック。
const lockFile = ".lock"

// sessionPrefix は結びつける前のセッションの参照の種類。
const sessionPrefix = "session:"

// Endpoint は辺の端点。書く側は Ref と CWD (その作業のセッションを起こしたディレクトリ) を渡し、Repo は Add が
// CWD の main worktree のパス (git の外なら CWD) で埋める。BoundFrom は Bind が session:<名前> を sid:<id> に
// 書き換えたときの元の参照 (結びついていなければ空)。
type Endpoint struct {
	Ref       string `json:"ref"`
	CWD       string `json:"cwd"`
	Repo      string `json:"repo"`
	BoundFrom string `json:"bound_from,omitempty"`
}

// written は書いたときの参照。id はこの値から決める。
func (ep Endpoint) written() string {
	if ep.BoundFrom != "" {
		return ep.BoundFrom
	}
	return ep.Ref
}

// Edge は辺 1 本の記録。
type Edge struct {
	ID        string    `json:"id"`
	Before    Endpoint  `json:"before"`
	After     Endpoint  `json:"after"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// Add は辺を書く。同じ辺があれば created_at と結びついた端点を残して書き直す (note と cwd、repo は後から書いた側)。
// 同じ辺は、書いたときの参照の組が同じ辺か、今の参照の組が同じ辺。
func (s Store) Add(before, after Endpoint, note string, now time.Time) (Added, error) {
	if err := s.normalize(&before, &after); err != nil {
		return Added{}, err
	}
	before.Repo, after.Repo = gitrepo.RepoKey(before.CWD), gitrepo.RepoKey(after.CWD)
	var added Added
	err := s.locked(func(dir string) error {
		e := Edge{ID: edgeID(before.Ref, after.Ref), Before: before, After: after, Note: strings.TrimSpace(note), CreatedAt: now}
		existing, found := s.match(dir, before.Ref, after.Ref)
		if found {
			e.ID, e.CreatedAt = existing.ID, existing.CreatedAt
			e.Before.Ref, e.Before.BoundFrom = existing.Before.Ref, existing.Before.BoundFrom
			e.After.Ref, e.After.BoundFrom = existing.After.Ref, existing.After.BoundFrom
		}
		added = Added{Created: !found, Edge: e}
		return write(dir, e)
	})
	return added, err
}

// Remove は before と after の辺を消す。書いたときの参照の組でも、結びついた後の参照の組でも当たる。
// 無い辺は消さずに Removed を false で返す。
func (s Store) Remove(before, after Endpoint) (Removed, error) {
	if err := s.normalize(&before, &after); err != nil {
		return Removed{}, err
	}
	removed := Removed{ID: edgeID(before.Ref, after.Ref)}
	err := s.locked(func(dir string) error {
		existing, found := s.match(dir, before.Ref, after.Ref)
		if !found {
			return nil
		}
		removed.ID = existing.ID
		if err := os.Remove(filepath.Join(dir, existing.ID+".json")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		removed.Removed = true
		return nil
	})
	return removed, err
}

// Pick は session:<名前> の端点を結びつけるセッションを選ぶ。createdAt は辺を書いた時刻。結びつけるセッションが
// まだ無ければ ok が false。
type Pick func(name string, createdAt time.Time) (sessionID string, ok bool)

// Bind は結びついていない session:<名前> の端点を pick が選んだセッションの sid:<id> に書き換え、書き換えた辺を
// 返す。結びついた端点は書き換えない。書き換えると今の参照の組が別の辺と重なるときは、同じ依存なので
// 結びつける側の辺を消して 1 本にする。
func (s Store) Bind(pick Pick) ([]Edge, error) {
	if s.Dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.Dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var bound []Edge
	err := s.locked(func(dir string) error {
		edges := readAll(dir)
		current := map[[2]string]bool{}
		for _, e := range edges {
			current[[2]string{e.Before.Ref, e.After.Ref}] = true
		}
		var errs []error
		for _, e := range edges {
			next, changed := bindEdge(e, pick)
			if !changed {
				continue
			}
			pair := [2]string{next.Before.Ref, next.After.Ref}
			delete(current, [2]string{e.Before.Ref, e.After.Ref})
			if current[pair] {
				if err := os.Remove(filepath.Join(dir, e.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			if err := write(dir, next); err != nil {
				errs = append(errs, err)
				continue
			}
			current[pair] = true
			bound = append(bound, next)
		}
		return errors.Join(errs...)
	})
	return bound, err
}

// bindEdge は e の結びついていない端点を pick で結びつけた辺と、書き換えたかを返す。両端が同じ参照になる
// 結びつけはしない。
func bindEdge(e Edge, pick Pick) (Edge, bool) {
	changed := false
	for _, ep := range []*Endpoint{&e.Before, &e.After} {
		name, ok := strings.CutPrefix(ep.Ref, sessionPrefix)
		if !ok || ep.BoundFrom != "" {
			continue
		}
		id, ok := pick(name, e.CreatedAt)
		if !ok {
			continue
		}
		next := ref.SessionID(id)
		if other := otherEnd(&e, ep); other.Ref == next {
			continue
		}
		ep.BoundFrom, ep.Ref = ep.Ref, next
		changed = true
	}
	return e, changed
}

func otherEnd(e *Edge, ep *Endpoint) *Endpoint {
	if ep == &e.Before {
		return &e.After
	}
	return &e.Before
}

// Find は辺のうち、どちらかの端点が repo に、どちらかの端点が ref に一致するものを登録の順に返す。
// 参照は今の参照で比べる。空の条件は絞らない。該当なしは空の配列。壊れたファイルは読み飛ばす。
func (s Store) Find(repo, ref string) ([]Edge, error) {
	edges := []Edge{}
	if s.Dir == "" {
		return edges, nil
	}
	if _, err := os.Stat(s.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return edges, nil
		}
		return nil, err
	}
	for _, e := range readAll(s.Dir) {
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

// match は参照の組 (before、after) の辺を探す。書いたときの参照の組の id のファイルを先に見て、無ければ今の
// 参照の組が同じ辺を探す。呼び出し側がロックを持つ。
func (s Store) match(dir, before, after string) (Edge, bool) {
	if e, ok := readEdge(filepath.Join(dir, edgeID(before, after)+".json")); ok {
		return e, true
	}
	for _, e := range readAll(dir) {
		if e.Before.Ref == before && e.After.Ref == after {
			return e, true
		}
	}
	return Edge{}, false
}

// normalize は両端を検証して整える (参照の前後の空白を落とす)。
func (s Store) normalize(before, after *Endpoint) error {
	if s.Dir == "" {
		return errors.New("edges の置き場所が無い")
	}
	for _, ep := range []*Endpoint{before, after} {
		if err := normalizeEndpoint(ep); err != nil {
			return err
		}
	}
	if before.Ref == after.Ref {
		return fmt.Errorf("%s から自分自身への辺は書かない", before.Ref)
	}
	return nil
}

// normalizeEndpoint は参照の前後の空白を落とし、参照の形と cwd が絶対パスであることを確かめる。結びつけの記録は
// 書く側からは受け取らない。
func normalizeEndpoint(ep *Endpoint) error {
	ep.Ref = strings.TrimSpace(ep.Ref)
	ep.BoundFrom = ""
	if !ref.Endpoint(ep.Ref) {
		return fmt.Errorf("参照 %q は session:<名前> / sid:<session_id> / issue:<owner>/<repo>#<番号> / pr:<owner>/<repo>#<番号> / task:<session_id>/<task_id> のどれでもない", ep.Ref)
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

// locked は edges/ を作り、ロックを取って fn を呼ぶ。
func (s Store) locked(fn func(dir string) error) error {
	dir := s.Dir
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("edges の作成に失敗: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- edges/ 配下の固定のパス
	if err != nil {
		return fmt.Errorf("ロックを開けない: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("ロックを取れない: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn(dir)
}

func write(dir string, e Edge) error {
	return statefile.WriteJSONAtomic(filepath.Join(dir, e.ID+".json"), ".edge.*", e)
}

// readAll は dir の辺を読む。ロックと書きかけの一時ファイル (先頭が .)、壊れたファイルは読み飛ばす。
func readAll(dir string) []Edge {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var edges []Edge
	for _, entry := range entries {
		if !isEdgeFile(entry) {
			continue
		}
		if e, ok := readEdge(filepath.Join(dir, entry.Name())); ok {
			edges = append(edges, e)
		}
	}
	return edges
}

// isEdgeFile は書きかけの一時ファイルとロック (先頭が .) を除いた辺のファイルか。
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
