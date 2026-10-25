// Package parking は agent がタスクの途中で見つけた脇のメモ (パーキングロット、#89) を XDG state 配下に
// 置く (#121)。置くのは `agents-daemon park add`、取り出すのは依存図の pane とファイルのメモの通知 (`park list`)。
//
//	parking/<id>.json   1 件 1 ファイル
//
// repo と正規化した title が同じ open のメモがあれば、新しいメモを作らずに sources へ足す。同じ置き方が二重に
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
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// 置ける種類。進めるのに要る判断と危ない操作の確認は置かずに聞く (#89)。
const (
	KindAside      = "aside"
	KindScope      = "scope"
	KindPreference = "preference"
)

// メモの出自。種類 (kind) とは別の軸。
const (
	// OriginPlaced は model が park tool (park add) で置いたメモ。
	OriginPlaced = "placed"
	// OriginAdopted は終了したセッションに残った未完了のタスクを pane の操作 (park adopt) で移したメモ。
	OriginAdopted = "adopted"
)

// 宛先 (Item.HandedTo) の形。peer と self は sid:<session_id>、新しく起こすセッションは起こす先の repo:<絶対パス>。
const (
	destinationSession = "sid:"
	destinationRepo    = "repo:"
)

// ToSession は session ID のセッションへ渡すときの宛先。
func ToSession(sessionID string) string { return destinationSession + sessionID }

// ToRepo は repo で新しく起こすセッションへ渡すときの宛先。起こしたセッションの ID は渡した後に分かるので、
// 宛先は起こす先の repo のまま書き直さない (取ってから書き直すまでの間に宛先の違うメモが見えるのを避ける)。
func ToRepo(path string) string { return destinationRepo + path }

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

// lockFile は parking/ で add と Set を直列にするロック。まとめる判定と書き込みの間に
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
	ID string `json:"id"`
	// Repo は置いたときの cwd の main worktree のパス (git の外なら cwd)。依存図の repo の鍵と同じ。
	Repo string `json:"repo"`
	Entry
	// Origin は出自 (OriginPlaced / OriginAdopted)。同じメモにまとめても最初のメモのまま。
	Origin  string   `json:"origin"`
	Sources []Source `json:"sources"`
	Status  string   `json:"status"`
	// HandedTo は handed にしたときの宛先 (ToSession / ToRepo の形)。handed でなければ空。
	HandedTo string `json:"handed_to,omitempty"`
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
// 無ければ origin の出自で新しいメモを作る。同じメモかは repo と title で見て、出自は見ない (同じ脇の話題を
// 2 つに分けない)。書いた後のメモを返す。
func (s Store) add(repo string, e Entry, origin string, src Source) (Added, error) {
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
	err := s.locked(func(dir string) error {
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
		added = Added{Created: true, Item: Item{ID: id, Repo: repo, Entry: e, Origin: origin, Sources: []Source{src}, Status: StatusOpen}}
		return write(dir, added.Item)
	})
	return added, err
}

// Set はメモ id の status を書き換え、書いた後のメモと書き換える前の status を返す。
// from が空でなければ、ファイルの status が from のときだけ書き換える (違えばエラー)。読んでから書くまでロックの中なので、
// 同じメモを 2 つのセッションが同時に取ろうとしても通るのは 1 つだけ (models/handoff の Claim)。
// to は宛先で、handed にするときは要り、それ以外では渡さない。宛先は status と同じ書き込みで書き、handed でなくなれば
// 空にする (別の書き込みにすると宛先の無い handed が見える。models/handoff の HandedHasDestination)。
func (s Store) Set(id, status, from, to string) (Item, string, error) {
	if s.Dir == "" {
		return Item{}, "", errors.New("parking の置き場所が無い")
	}
	if err := validStatus(status); err != nil {
		return Item{}, "", err
	}
	if from != "" {
		if err := validStatus(from); err != nil {
			return Item{}, "", err
		}
	}
	if err := validDestination(status, to); err != nil {
		return Item{}, "", err
	}
	if !statefile.ValidSessionID(id) {
		return Item{}, "", fmt.Errorf("id %q はファイル名に使えない", id)
	}
	var item Item
	var before string
	err := s.locked(func(dir string) error {
		var ok bool
		item, ok = readItem(filepath.Join(dir, id+".json"))
		if !ok {
			return fmt.Errorf("メモ %s が無い", id)
		}
		if from != "" && item.Status != from {
			return fmt.Errorf("メモ %s の status は %s (期待 %s) なので書き換えない", id, item.Status, from)
		}
		before, item.Status, item.HandedTo = item.Status, status, to
		return write(dir, item)
	})
	return item, before, err
}

// DiscardBefore は open のメモのうち、最後に置かれた時刻 (sources の最大の at) が before より前の
// ものを discarded にし、捨てたメモを id の順に返す。該当なしは空の配列。pane の「古いメモをまとめて捨てる」が
// 呼び、「古い」の日数は呼び出し側 (mod) が持つ。
func (s Store) DiscardBefore(before time.Time) ([]Item, error) {
	if s.Dir == "" {
		return nil, errors.New("parking の置き場所が無い")
	}
	discarded := []Item{}
	err := s.locked(func(dir string) error {
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

// list はメモを id の順 (置いた順) に返す。壊れたファイルは読み飛ばす。
func (s Store) list() ([]Item, error) {
	if s.Dir == "" {
		return nil, nil
	}
	return readDir(s.Dir), nil
}

// Find はメモのうち repo と status が一致するものを返す。空の条件は絞らない。該当なしは空の配列。
func (s Store) Find(repo, status string) ([]Item, error) {
	if status != "" {
		if err := validStatus(status); err != nil {
			return nil, err
		}
	}
	items, err := s.list()
	if err != nil {
		return nil, err
	}
	return Filter(items, repo, status), nil
}

// Filter は items のうち repo と status が一致するものを返す。空の条件は絞らない。該当なしは空の配列。
func Filter(items []Item, repo, status string) []Item {
	matched := []Item{}
	for _, item := range items {
		if (repo == "" || item.Repo == repo) && (status == "" || item.Status == status) {
			matched = append(matched, item)
		}
	}
	return matched
}

// Mentioned は items のうち text に id が書かれたメモを、書かれた順に返す。items に無い id (消えたメモ) は落とす。
// タスクの description からそのタスクが扱うメモを引くのに使い、メモの側にはタスクを書き戻さない。
func Mentioned(items []Item, text string) []Item {
	byID := map[string]Item{}
	for _, item := range items {
		byID[item.ID] = item
	}
	matched := []Item{}
	for _, id := range mentionedIDs(text) {
		if item, ok := byID[id]; ok {
			matched = append(matched, item)
		}
	}
	return matched
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
	case KindAside, KindScope, KindPreference:
		return nil
	}
	return fmt.Errorf("kind %q は aside / scope / preference のどれでもない", kind)
}

// validDestination は status に合う宛先かを確かめる。handed には sid:<session_id> か repo:<絶対パス> が要り、
// それ以外の status には宛先を渡さない。
func validDestination(status, to string) error {
	if status != StatusHanded {
		if to != "" {
			return fmt.Errorf("宛先 %q は handed にするときだけ渡す", to)
		}
		return nil
	}
	if id, ok := strings.CutPrefix(to, destinationSession); ok && statefile.ValidSessionID(id) {
		return nil
	}
	if path, ok := strings.CutPrefix(to, destinationRepo); ok && filepath.IsAbs(path) {
		return nil
	}
	return fmt.Errorf("handed の宛先 %q は sid:<session_id> / repo:<絶対パス> のどれでもない", to)
}

// locked は parking/ を作り、ロックを取って fn を呼ぶ。
func (s Store) locked(fn func(dir string) error) error {
	dir := s.Dir
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
// id の時刻を出さず、sources の at を JST で出す (parking.md「時刻の書き方」)。
func newID(at time.Time) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("id の乱数を作れない: %w", err)
	}
	return at.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b), nil
}

// idPattern は newID が作る id の形。前後に英数字が続くものは別の文字列とみなす。
var idPattern = regexp.MustCompile(`\b\d{8}T\d{6}-[0-9a-f]{8}\b`)

// mentionedIDs は text に書かれたメモの id を、現れた順に重複なく返す。無ければ空の配列。
func mentionedIDs(text string) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range idPattern.FindAllString(text, -1) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
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
