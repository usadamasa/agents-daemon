// Package ledger はセッション台帳を XDG state 配下に置く。書くのは daemon だけで、tick ごとに status.json に
// 書いた pane と context/ のセッション名から、見えたセッションを書く。
//
//	sessions/<session_id>.json   セッション 1 つ (名前、cwd、repo、最初と最後に見た時刻)
//
// first_seen は最初に書いた時刻のまま動かず、last_seen は pane が見えている間 tick ごとに進む。daemon は台帳を
// 使って `edge add` の session:<名前> の端点を session ID に結びつける (Pick、edge の Bind)。
// 選び方は Pick の doc。同名のセッションが辺を書く前から 2 つ動いていて古い方を待たせたかった、のような稀な
// 取り違えは、`edge rm` してから sid:<session_id> で `edge add` し直して直す。
package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention は見えなくなったセッションを残す期間。消すのは daemon の housekeeping (Prune) で、mtime で判定する。
// 結びつけは tick ごとなので、結びつけに要るのは生きている間とその直後だけ。それより長く、辺 (edge の
// Retention) と同じ 7 日にそろえ、依存図に出うる間はそのセッションを引けるようにする。
const Retention = 7 * 24 * time.Hour

// Session は台帳のセッション 1 つ。
type Session struct {
	SessionID string `json:"session_id"`
	// Name はセッション名 (`claude --name`、statusline の session_name)。名前が無ければ空。
	Name string `json:"name,omitempty"`
	CWD  string `json:"cwd"`
	// Repo は cwd の repo の鍵 (gitrepo.RepoKey)。
	Repo      string    `json:"repo"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Seen は tick で見えたセッション 1 つ。
type Seen struct {
	SessionID string
	CWD       string
	Name      string
}

// Store は sessions/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
	// repoKey は cwd の repo の鍵を求める (テストが git を起こさずに差し替える継ぎ目)。
	repoKey func(cwd string) string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "sessions"), repoKey: gitrepo.RepoKey}
}

// Observe は見えたセッションを書く。既にあれば first_seen を残し、last_seen を now に進める。名前は空でなければ
// 書き直し、repo は cwd が変わったときだけ求め直す (git を tick ごとに起こさない)。
func (s Store) Observe(seen []Seen, now time.Time) error {
	if s.Dir == "" {
		return errors.New("sessions の置き場所が無い")
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return fmt.Errorf("sessions の作成に失敗: %w", err)
	}
	var errs []error
	for _, x := range seen {
		if !statefile.ValidSessionID(x.SessionID) {
			errs = append(errs, fmt.Errorf("session ID %q はファイル名に使えない", x.SessionID))
			continue
		}
		path := filepath.Join(s.Dir, x.SessionID+".json")
		rec, found := read(path)
		if !found {
			rec = Session{SessionID: x.SessionID, FirstSeen: now}
		}
		if !found || rec.CWD != x.CWD || rec.Repo == "" {
			rec.CWD, rec.Repo = x.CWD, s.repoKey(x.CWD)
		}
		if x.Name != "" {
			rec.Name = x.Name
		}
		rec.LastSeen = now
		if err := statefile.WriteJSONAtomic(path, ".session.*", rec); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// List は台帳のセッションを session ID の順に返す。壊れたファイルと書きかけの一時ファイルは読み飛ばす。
func (s Store) List() ([]Session, error) {
	if s.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name[0] == '.' || filepath.Ext(name) != ".json" {
			continue
		}
		if rec, ok := read(filepath.Join(s.Dir, name)); ok {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

// Prune は Retention のあいだ見えなかったセッション (と消し損ねた一時ファイル) を mtime で消し、消した数を返す。
func (s Store) Prune(now time.Time) (int, error) {
	return statefile.PruneDir(s.Dir, now.Add(-Retention))
}

// Pick は session:<名前> の端点を結びつけるセッションを選ぶ。候補は名前が一致し、辺を書いた時刻 createdAt に
// 生きていたか後から現れた (last_seen が createdAt 以後の) セッション。辺を書く前に終わった同名のセッションは
// 選ばない。
//   - 辺を書いた時点で動いていた候補 (first_seen が createdAt 以前) があれば、first_seen の最も新しいものを選ぶ。
//     渡す側は `claude --name` で起こしてすぐ `edge add` を書くので、直前に起きた同名のセッションが渡した相手で、
//     まだ残っている古い同名のセッションではない
//   - 無ければ createdAt より後に最初に現れたもの (first_seen の最も早いもの) を選ぶ
//
// 同じ時刻なら session ID の順。候補が無ければ ok が false。
func Pick(sessions []Session, name string, createdAt time.Time) (sessionID string, ok bool) {
	var running, later *Session
	for i := range sessions {
		x := &sessions[i]
		if x.Name != name || x.LastSeen.Before(createdAt) {
			continue
		}
		if !x.FirstSeen.After(createdAt) {
			if running == nil || x.FirstSeen.After(running.FirstSeen) || (x.FirstSeen.Equal(running.FirstSeen) && x.SessionID < running.SessionID) {
				running = x
			}
			continue
		}
		if later == nil || x.FirstSeen.Before(later.FirstSeen) || (x.FirstSeen.Equal(later.FirstSeen) && x.SessionID < later.SessionID) {
			later = x
		}
	}
	switch {
	case running != nil:
		return running.SessionID, true
	case later != nil:
		return later.SessionID, true
	default:
		return "", false
	}
}

func read(path string) (Session, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- sessions/ 配下の検証済みのパス
	if err != nil {
		return Session{}, false
	}
	var rec Session
	if err := json.Unmarshal(data, &rec); err != nil || rec.SessionID == "" {
		return Session{}, false
	}
	return rec, true
}
