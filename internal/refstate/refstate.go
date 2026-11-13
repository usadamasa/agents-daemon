// Package refstate は辺の端点の外部の参照 (issue / PR) の状態の写しを XDG state 配下に置く (#416)。書くのは daemon
// だけで、gh で引いた状態を書く。読むのは依存図 (deps) で、merged / closed の参照を完了にし、dropped の参照を
// 取りやめにし、引いた題を参照のノードの題にする。
//
//	refs/<id>.json   参照 1 つ (参照、状態、題、open の PR の段、PR の head branch、引いた時刻)
//
// id は参照から決める (参照は / と # を含み、そのままではファイル名にできない)。引き直すかどうかは Due が決め、
// 手順の性質は models/refstate が持つ。
package refstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// 状態の語。閉じた参照は、待っていたものが届いた (済んだ) か、届かずに閉じた (取りやめ) かで分ける。
const (
	StateOpen = "open"
	// StateMerged はマージされた PR。
	StateMerged = "merged"
	// StateClosed は completed で閉じた issue。
	StateClosed = "closed"
	// StateDropped はマージせずに閉じた PR と、not planned / duplicate で閉じた issue。
	StateDropped = "dropped"
)

const (
	// RefreshInterval は open の参照を引き直す間隔と、gh が失敗した参照を引き直すまでの間隔。PR のマージは人が
	// 依存図を見て次の作業へ移る速さで反映されれば足り、gh の呼び出しを参照 12 個で毎分 2〜3 回に抑える。
	RefreshInterval = 5 * time.Minute
	// Retention は引き直していない写しを残す期間。消すのは daemon の housekeeping (Prune) で、mtime で判定する。
	// open の写しは引き直すたびに書き直すので、残り続けるのは閉じた参照の写しと、辺が消えて引かなくなった
	// 参照だけ。辺 (edge の Retention) と同じ 7 日にそろえ、辺が残る間は写しも残す。消えた写しは、辺が残っていれば
	// 次の引き直しで書き直す。
	Retention = 7 * 24 * time.Hour
)

// open の PR の段の語。空は「分からない」か「当てはまらない」で、どちらも依存図に何も出さない。
const (
	CIFailing = "failing"
	CIRunning = "running"
	CIPassing = "passing"

	ReviewRequired         = "required"
	ReviewChangesRequested = "changes_requested"
	ReviewApproved         = "approved"

	MergeConflicting = "conflicting"
	MergeBehind      = "behind"
	MergeClean       = "clean"
)

// PR は open の PR の段。gh の値を畳んだ語で持つ。ゼロ値は段の欄ができる前の写しと同じで、何も断定しない。
type PR struct {
	Draft  bool   `json:"draft,omitempty"`
	CI     string `json:"ci,omitempty"`
	Review string `json:"review,omitempty"`
	Merge  string `json:"merge,omitempty"`
}

// Record は参照 1 つの写し。
type Record struct {
	Ref   string `json:"ref"`
	State string `json:"state"`
	// Title は issue / PR の題。閉じた参照は引き直さないので、閉じた後に変わった題は写らない。
	Title string `json:"title,omitempty"`
	PR    PR     `json:"pr,omitzero"`
	// HeadBranch は PR の head branch。PR を出したセッションを branch の一致で引くのに使う (#487)。
	HeadBranch string    `json:"head_branch,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Done は済んだ状態 (merged / closed) か。取りやめは閉じているが済んでいない。
func Done(state string) bool {
	return state == StateMerged || state == StateClosed
}

// settled は閉じた状態 (済んだか取りやめ) か。
func settled(state string) bool {
	return Done(state) || state == StateDropped
}

// Due は参照を引き直すか。写しが無ければ引く。閉じた参照は引き直さない (閉じた状態は戻らないとみなす)。
// open は前回から RefreshInterval 経ったら引く。
func Due(rec Record, found bool, now time.Time) bool {
	if !found {
		return true
	}
	if settled(rec.State) {
		return false
	}
	return !now.Before(rec.CheckedAt.Add(RefreshInterval))
}

// Store は refs/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "refs")}
}

// Put は写しを書く。同じ参照の写しは書き直す。
func (s Store) Put(rec Record) error {
	if s.Dir == "" {
		return errors.New("refs の置き場所が無い")
	}
	if _, ok := ref.ParseItem(rec.Ref); !ok {
		return fmt.Errorf("参照 %q は issue / PR の参照でない", rec.Ref)
	}
	if rec.State != StateOpen && !settled(rec.State) {
		return fmt.Errorf("%s の状態 %q を知らない", rec.Ref, rec.State)
	}
	return statefile.WriteJSONAtomic(filepath.Join(s.Dir, id(rec.Ref)+".json"), ".ref.*", rec)
}

// List は写しを参照ごとに返す。置き場が無ければ空。壊れたファイルと書きかけの一時ファイルは読み飛ばす。
func (s Store) List() (map[string]Record, error) {
	out := map[string]Record{}
	if s.Dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name[0] == '.' || filepath.Ext(name) != ".json" {
			continue
		}
		if rec, ok := read(filepath.Join(s.Dir, name)); ok {
			out[rec.Ref] = rec
		}
	}
	return out, nil
}

// States は参照 → 状態。依存図が読む形。
func (s Store) States() (map[string]string, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for r, rec := range list {
		out[r] = rec.State
	}
	return out, nil
}

// Titles は参照 → 題。題の無い写しは載せない。依存図が読む形。
func (s Store) Titles() (map[string]string, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for r, rec := range list {
		if rec.Title != "" {
			out[r] = rec.Title
		}
	}
	return out, nil
}

// PRs は参照 → PR の段。段の無い写しは載せない。依存図が読む形。
func (s Store) PRs() (map[string]PR, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	out := map[string]PR{}
	for r, rec := range list {
		if rec.PR != (PR{}) {
			out[r] = rec.PR
		}
	}
	return out, nil
}

// Prune は Retention のあいだ書き直していない写し (と消し損ねた一時ファイル) を mtime で消し、消した数を返す。
func (s Store) Prune(now time.Time) (int, error) {
	return statefile.PruneDir(s.Dir, now.Add(-Retention))
}

func id(r string) string {
	sum := sha256.Sum256([]byte(r))
	return hex.EncodeToString(sum[:8])
}

func read(path string) (Record, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- refs/ 配下の走査で得たパス
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil || rec.Ref == "" {
		return Record{}, false
	}
	return rec, true
}
