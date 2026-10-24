// Package parking は agent がタスクの途中で見つけた脇のメモ (パーキングロット、#89) を XDG state 配下に
// 置く (#121)。置くのは `agents-daemon park add`、取り出すのはタスクのボードと band。
//
//	parking/<domain>/<id>.json   1 件 1 ファイル
//
// domain (home / work) で置き場を分け、私用と業務のメモを混ぜない (internal/domain)。domain、repo、
// 正規化した title が同じ open のメモがあれば、新しいメモを作らずに sources へ足す。同じ置き方が二重に
// 走ってもメモは増えない (Idempotent Receiver)。
package parking

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// 置ける種類。進めるのに要る判断と危ない操作の確認は置かずに聞く (#89)。
const (
	KindAside      = "aside"
	KindScope      = "scope"
	KindPreference = "preference"
	// KindTask は終了したセッションに残った未完了のタスク。ボードの操作 (park adopt) だけが置き、
	// model の tool には出さない。
	KindTask = "task"
)

const (
	StatusOpen      = "open"
	StatusHanded    = "handed"
	StatusDiscarded = "discarded"
)

// 保持期間。消すのは daemon の housekeeping (Prune) で、mtime で判定する。同じメモが置き直されると
// sources への追記で mtime が進むので、open の期間はそこから数え直す。
const (
	// OpenRetention は open のメモを残す期間。週次の棚卸しを 10 回逃しても残る長さ。それより古い
	// 脇のメモは文脈が失われている。
	OpenRetention = 90 * 24 * time.Hour
	// ClosedRetention は handed / discarded のメモを残す期間。「最近片付けたもの」として見えれば足りる。
	ClosedRetention = 7 * 24 * time.Hour
)

// lockFile は domain のディレクトリで add と Set を直列にするロック。まとめる判定と書き込みの間に
// 別の park add が割り込んで同じメモが 2 つできるのを防ぐ。
const lockFile = ".lock"

// Entry は置く側が決める中身。
type Entry struct {
	Kind string `json:"kind"`
	// Title は人が書いた形 (前後の空白だけ落とす)。同じメモかの判定は normalize した鍵で見る。
	Title        string   `json:"title"`
	Detail       string   `json:"detail,omitempty"`
	Files        []string `json:"files,omitempty"`
	DefaultTaken string   `json:"default_taken,omitempty"`
}

// Source はメモを置いたときのセッションと、そのとき進行中だったタスク。
type Source struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id,omitempty"`
	CWD       string `json:"cwd"`
	// Detail は既存のメモにまとめたとき、後から置いた側が書いた detail (メモの detail と違うときだけ)。
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Item は置かれたメモ 1 つの記録。
type Item struct {
	ID     string        `json:"id"`
	Domain domain.Domain `json:"domain"`
	// Repo は置いたときの cwd の main worktree のパス (git の外なら cwd)。ボードの行の鍵と同じ。
	Repo string `json:"repo"`
	Entry
	Sources []Source `json:"sources"`
	Status  string   `json:"status"`
}

// Store は parking/ の置き場所を持つ。ゼロ値は Dir が空で、書き込みはエラー、読み込みは空になる。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "parking")}
}

// Added は Park と Adopt の結果。置いた側が「既に置かれていたメモ」と伝えられるよう、新しく作ったかを持つ。
type Added struct {
	Created bool `json:"created"`
	Item    Item `json:"item"`
}

// add はメモを置く。同じメモが open であれば src を sources に足し (同じ session、task、detail の組は足さない)、
// 無ければ新しいメモを作る。書いた後のメモを返す。
func (s Store) add(d domain.Domain, repo string, e Entry, src Source) (Added, error) {
	if s.Dir == "" {
		return Added{}, errors.New("parking の置き場所が無い")
	}
	if err := validKind(e.Kind); err != nil {
		return Added{}, err
	}
	e.Title = strings.TrimSpace(e.Title)
	if e.Title == "" {
		return Added{}, errors.New("title が空")
	}
	if !statefile.ValidSessionID(src.SessionID) {
		return Added{}, fmt.Errorf("session ID %q はファイル名に使えない", src.SessionID)
	}
	var added Added
	err := s.locked(d, func(dir string) error {
		key := normalize(e.Title)
		for _, existing := range readDir(dir) {
			if existing.Status == StatusOpen && existing.Repo == repo && normalize(existing.Title) == key {
				if e.Detail != existing.Detail {
					src.Detail = e.Detail
				}
				if !hasSource(existing.Sources, src) {
					existing.Sources = append(existing.Sources, src)
				}
				added = Added{Item: existing}
				return write(dir, existing)
			}
		}
		id, err := newID(src.At)
		if err != nil {
			return err
		}
		added = Added{Created: true, Item: Item{ID: id, Domain: d, Repo: repo, Entry: e, Sources: []Source{src}, Status: StatusOpen}}
		return write(dir, added.Item)
	})
	return added, err
}

// Set は domain のメモ id の status を書き換え、書いた後のメモと書き換える前の status を返す。
func (s Store) Set(d domain.Domain, id, status string) (Item, string, error) {
	if s.Dir == "" {
		return Item{}, "", errors.New("parking の置き場所が無い")
	}
	if err := validStatus(status); err != nil {
		return Item{}, "", err
	}
	if !statefile.ValidSessionID(id) {
		return Item{}, "", fmt.Errorf("id %q はファイル名に使えない", id)
	}
	var item Item
	var from string
	err := s.locked(d, func(dir string) error {
		var ok bool
		item, ok = readItem(filepath.Join(dir, id+".json"))
		if !ok {
			return fmt.Errorf("%s のメモ %s が無い", d, id)
		}
		from, item.Status = item.Status, status
		return write(dir, item)
	})
	return item, from, err
}

// DiscardBefore は domain の open のメモのうち、最後に置かれた時刻 (sources の最大の at) が before より前の
// ものを discarded にし、捨てたメモを id の順に返す。該当なしは空の配列。ボードの「古いメモをまとめて捨てる」が
// 呼び、「古い」の日数は呼び出し側 (mod) が持つ。
func (s Store) DiscardBefore(d domain.Domain, before time.Time) ([]Item, error) {
	if s.Dir == "" {
		return nil, errors.New("parking の置き場所が無い")
	}
	discarded := []Item{}
	err := s.locked(d, func(dir string) error {
		for _, item := range readDir(dir) {
			if item.Status != StatusOpen || !lastPlacedAt(item).Before(before) {
				continue
			}
			item.Status = StatusDiscarded
			if err := write(dir, item); err != nil {
				return err
			}
			discarded = append(discarded, item)
		}
		return nil
	})
	return discarded, err
}

// lastPlacedAt はメモが最後に置かれた時刻。置き直されたメモは足した source の時刻で見る。
func lastPlacedAt(item Item) time.Time {
	var last time.Time
	for _, src := range item.Sources {
		if src.At.After(last) {
			last = src.At
		}
	}
	return last
}

// list は domain のメモを id の順 (置いた順) に返す。壊れたファイルは読み飛ばす。
func (s Store) list(d domain.Domain) ([]Item, error) {
	if s.Dir == "" {
		return nil, nil
	}
	return readDir(filepath.Join(s.Dir, string(d))), nil
}

// Find は domain のメモのうち repo と status が一致するものを返す。空の条件は絞らない。該当なしは空の配列。
func (s Store) Find(d domain.Domain, repo, status string) ([]Item, error) {
	if status != "" {
		if err := validStatus(status); err != nil {
			return nil, err
		}
	}
	items, err := s.list(d)
	if err != nil {
		return nil, err
	}
	matched := []Item{}
	for _, item := range items {
		if (repo == "" || item.Repo == repo) && (status == "" || item.Status == status) {
			matched = append(matched, item)
		}
	}
	return matched, nil
}

// PlacedDuring は items のうち、sessionID のセッションが taskID のタスクの途中で置いたメモを返す。
// 別のセッションが置いたメモにまとめたものも、足した source で引ける。該当なしは空の配列。
func PlacedDuring(items []Item, sessionID, taskID string) []Item {
	matched := []Item{}
	for _, item := range items {
		for _, src := range item.Sources {
			if src.SessionID == sessionID && src.TaskID == taskID {
				matched = append(matched, item)
				break
			}
		}
	}
	return matched
}

// Request は `park add` の stdin。置く中身と、置いたセッションの ID。
type Request struct {
	Entry
	SessionID string `json:"session_id"`
}

// ReadRequest は r から Request を読む。
func ReadRequest(r io.Reader) (Request, error) {
	var req Request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return Request{}, fmt.Errorf("stdin の JSON を読めない: %w", err)
	}
	return req, nil
}

// validStatus は status が open / handed / discarded のどれかかを確かめる。
func validStatus(status string) error {
	switch status {
	case StatusOpen, StatusHanded, StatusDiscarded:
		return nil
	}
	return fmt.Errorf("status %q は open / handed / discarded のどれでもない", status)
}

func validKind(kind string) error {
	switch kind {
	case KindAside, KindScope, KindPreference, KindTask:
		return nil
	}
	return fmt.Errorf("kind %q は aside / scope / preference / task のどれでもない", kind)
}

// locked は domain のディレクトリを作り、ロックを取って fn を呼ぶ。
func (s Store) locked(d domain.Domain, fn func(dir string) error) error {
	dir := filepath.Join(s.Dir, string(d))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("parking の作成に失敗: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- parking/ 配下の固定のパス
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

// normalize は同じメモかを見るための title。空白 (全角を含む) をすべて落として小文字にする。
// 日本語の title は語の区切りに空白を使わないので、空白の有無や数の揺れを同じメモとみなす。
func normalize(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), ""))
}

// hasSource は同じ置き方 (session、task、detail の組) が既にあるか。同じ source でも detail が新しければ
// 後から分かった補足として残す。
func hasSource(sources []Source, src Source) bool {
	for _, s := range sources {
		if s.SessionID == src.SessionID && s.TaskID == src.TaskID && s.Detail == src.Detail {
			return true
		}
	}
	return false
}

// newID は置いた時刻と乱数で id を作る。時刻を先に置くので、名前の順が置いた順になる。
// 時刻は UTC にする。端末の時刻帯に左右されず、ファイル名の並びが時刻の並びと一致する。人に見せるときは
// id の時刻を出さず、sources の at を JST で出す (architecture.md「時刻の書き方」)。
func newID(at time.Time) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("id の乱数を作れない: %w", err)
	}
	return at.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b), nil
}

func write(dir string, item Item) error {
	return statefile.WriteJSONAtomic(filepath.Join(dir, item.ID+".json"), ".item.*", item)
}

// readDir は dir のメモを id の順に返す。ロックと書きかけの一時ファイル (先頭が .) は読まない。
func readDir(dir string) []Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	items := []Item{}
	for _, e := range entries {
		if !isItemFile(e) {
			continue
		}
		if item, ok := readItem(filepath.Join(dir, e.Name())); ok {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func isItemFile(e os.DirEntry) bool {
	name := e.Name()
	return !e.IsDir() && name[0] != '.' && filepath.Ext(name) == ".json"
}

func readItem(path string) (Item, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- parking/ 配下の検証済みのパス
	if err != nil {
		return Item{}, false
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil || item.ID == "" {
		return Item{}, false
	}
	return item, true
}
