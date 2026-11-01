package parking

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRepo = "/src/github.com/usadamasa/agents-daemon"

var testNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func aside(title string) Entry {
	return Entry{Kind: KindAside, Title: title, Detail: "detail", Files: []string{"a.go"}, DefaultTaken: "やらない"}
}

func source(sessionID, taskID string) Source {
	return Source{SessionID: sessionID, TaskID: taskID, CWD: testRepo, At: testNow}
}

func mustAdd(t *testing.T, s Store, repo string, e Entry, src Source) Item {
	t.Helper()
	added, err := s.add(repo, e, OriginPlaced, src)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}
	return added.Item
}

// 置いたメモは parking/<id>.json に open で書かれ、List で読める。
func TestAdd_writesOneFilePerItem(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, testRepo, aside("保持期間の根拠が無い"), source("s1", "3"))

	if item.Status != StatusOpen || item.Repo != testRepo || item.Kind != KindAside {
		t.Errorf("item = %+v", item)
	}
	if len(item.Sources) != 1 || item.Sources[0].TaskID != "3" {
		t.Errorf("sources = %+v", item.Sources)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, item.ID+".json")); err != nil {
		t.Errorf("ファイルが無い: %v", err)
	}
	got, err := s.list()
	if err != nil || len(got) != 1 || got[0].ID != item.ID || got[0].Title != "保持期間の根拠が無い" {
		t.Errorf("List() = %+v, %v", got, err)
	}
}

// PlacedDuring は session と task の組が sources にあるメモだけを残す。まとめたメモも、後から足した source で引ける。
func TestPlacedDuring(t *testing.T) {
	s := New(t.TempDir())
	during := mustAdd(t, s, testRepo, aside("a"), source("s1", "3"))
	mustAdd(t, s, testRepo, aside("b"), source("s2", "3"))
	merged := mustAdd(t, s, testRepo, aside("b"), source("s1", "3"))
	mustAdd(t, s, testRepo, aside("c"), source("s1", "4"))
	mustAdd(t, s, testRepo, aside("d"), source("s2", "3"))
	mustAdd(t, s, testRepo, aside("e"), source("s1", ""))
	items, _ := s.list()

	// 同じ時刻に置いたメモの id は乱数の部分で並ぶので、順は見ない。
	got := PlacedDuring(items, "s1", "3")
	ids := map[string]bool{}
	for _, item := range got {
		ids[item.ID] = true
	}
	if len(got) != 2 || !ids[during.ID] || !ids[merged.ID] {
		t.Errorf("PlacedDuring() = %+v, want %s と %s", got, during.ID, merged.ID)
	}
	if got := PlacedDuring(items, "s1", "9"); got == nil || len(got) != 0 {
		t.Errorf("該当なしは空の配列: %+v", got)
	}
}

// mentionedIDs は文面に書かれたメモの id を、現れた順に重複なく拾う。id の形の前後に英数字が続くものは拾わない。
func TestMentionedIDs(t *testing.T) {
	const a, b = "20261025T044319-7ed42d24", "20261019T120000-0a1b2c3d"
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"無い", "メモの id を書いていない", []string{}},
		{"空", "", []string{}},
		{"1 つ", "(メモ " + a + ")", []string{a}},
		{"2 つ", "メモ " + a + " と\nメモ " + b, []string{a, b}},
		{"重複", a + ", " + b + ", " + a, []string{a, b}},
		{"後ろに英数字が続く", a + "ff", []string{}},
		{"前に英数字が付く", "x" + a, []string{}},
		{"乱数の部分が大文字", "20261025T044319-7ED42D24", []string{}},
		{"乱数の部分が短い", "20261025T044319-7ed42d2", []string{}},
		{"時刻の区切りが無い", "20261025044319-7ed42d24", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mentionedIDs(tt.text)
			if got == nil || strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("mentionedIDs(%q) = %#v, want %#v", tt.text, got, tt.want)
			}
		})
	}
}

// Mentioned は文面に id が書かれたメモを、書かれた順に返す。items に無い id は落とす。
func TestMentioned(t *testing.T) {
	const a, b, gone = "20261025T044319-7ed42d24", "20261019T120000-0a1b2c3d", "20261019T130000-0a1b2c3e"
	items := []Item{{ID: a, Status: StatusHanded}, {ID: b, Status: StatusOpen}}
	got := Mentioned(items, "メモ "+b+" と "+gone+" と "+a)
	if len(got) != 2 || got[0].ID != b || got[1].ID != a || got[1].Status != StatusHanded {
		t.Errorf("Mentioned() = %+v", got)
	}
	if got := Mentioned(items, ""); got == nil || len(got) != 0 {
		t.Errorf("該当なしは空の配列: %#v", got)
	}
}

// Filter は repo と status が一致するメモを残す。空の条件は絞らない。
func TestFilter(t *testing.T) {
	items := []Item{{ID: "1", Repo: "r", Status: StatusOpen}, {ID: "2", Repo: "r", Status: StatusHanded}, {ID: "3", Repo: "s", Status: StatusOpen}}
	ids := func(items []Item) string {
		out := []string{}
		for _, item := range items {
			out = append(out, item.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(Filter(items, "", StatusOpen)); got != "1,3" {
		t.Errorf("status で絞る = %s", got)
	}
	if got := ids(Filter(items, "r", "")); got != "1,2" {
		t.Errorf("repo で絞る = %s", got)
	}
	if got := Filter(items, "x", ""); got == nil || len(got) != 0 {
		t.Errorf("該当なしは空の配列: %#v", got)
	}
}

// newID が作る id は mentionedIDs が拾う形と同じ。
func TestMentionedIDs_matchesNewID(t *testing.T) {
	id, err := newID(testNow)
	if err != nil {
		t.Fatalf("newID() error = %v", err)
	}
	if got := mentionedIDs("メモ " + id + " を扱う"); len(got) != 1 || got[0] != id {
		t.Errorf("mentionedIDs() = %v, want [%s]", got, id)
	}
}

// repo、正規化した title が同じ open のメモには sources を足し、新しいメモを作らない。
func TestAdd_mergesSameItem(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, testRepo, aside("保持期間の  根拠が無い"), source("s1", "3"))
	second := mustAdd(t, s, testRepo, aside(" 保持期間の根拠が無い "), source("s2", "1"))
	if second.ID != first.ID {
		t.Fatalf("別のメモになった: %s != %s", second.ID, first.ID)
	}
	// 同じ置き方 (同じ session と task) を二重に送っても sources は増えない。
	third := mustAdd(t, s, testRepo, aside("保持期間の根拠が無い"), source("s2", "1"))
	if len(third.Sources) != 2 {
		t.Errorf("sources = %+v, want 2 件", third.Sources)
	}
	if got, _ := s.list(); len(got) != 1 {
		t.Errorf("件数 = %d, want 1", len(got))
	}
}

// add は新しいメモを作ったか、既存のメモにまとめたかを返す。同じ置き方の二重送信もまとめた側になる。
func TestAdd_reportsCreated(t *testing.T) {
	s := New(t.TempDir())
	cases := []struct {
		src  Source
		want bool
	}{
		{source("s1", "3"), true},
		{source("s2", "1"), false},
		{source("s2", "1"), false},
	}
	for i, c := range cases {
		added, err := s.add(testRepo, aside("t"), OriginPlaced, c.src)
		if err != nil {
			t.Fatal(err)
		}
		if added.Created != c.want {
			t.Errorf("%d 回目の Created = %v, want %v", i+1, added.Created, c.want)
		}
	}
}

// まとめるときは、後から置いた側の detail が最初のメモと違えば sources に残す。同じなら残さない。
func TestAdd_keepsLaterDetailInSource(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, testRepo, aside("t"), source("s1", ""))
	same := mustAdd(t, s, testRepo, aside("t"), source("s2", ""))
	if same.Sources[1].Detail != "" {
		t.Errorf("同じ detail を sources に残した: %+v", same.Sources[1])
	}
	other := aside("t")
	other.Detail = "別のファイルでも同じ"
	merged := mustAdd(t, s, testRepo, other, source("s3", ""))
	if merged.Detail != "detail" || merged.Sources[2].Detail != "別のファイルでも同じ" {
		t.Errorf("merged = %+v", merged)
	}
	if merged.Sources[0].Detail != "" {
		t.Errorf("最初の source に detail を入れた: %+v", merged.Sources[0])
	}
}

// 同じ session と task から置き直しても、detail が既にあるどれとも違えば、その detail と時刻を source に残す。
// メモの detail か既にある source の detail と同じなら足さない (二重送信で増えない)。
func TestAdd_keepsNewDetailFromSameSource(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, testRepo, aside("t"), source("s1", "3"))
	later := aside("t")
	later.Detail = "後から分かった補足"
	src := source("s1", "3")
	src.At = testNow.Add(time.Hour)
	merged := mustAdd(t, s, testRepo, later, src)
	if len(merged.Sources) != 2 || merged.Sources[1].Detail != "後から分かった補足" || !merged.Sources[1].At.Equal(src.At) {
		t.Fatalf("sources = %+v", merged.Sources)
	}
	again := mustAdd(t, s, testRepo, later, src)
	if len(again.Sources) != 2 {
		t.Errorf("同じ detail の二重送信で増えた: %+v", again.Sources)
	}
	if first := mustAdd(t, s, testRepo, aside("t"), source("s1", "3")); len(first.Sources) != 2 {
		t.Errorf("メモと同じ detail で増えた: %+v", first.Sources)
	}
}

// 正規化した title は同じメモかを見る鍵にだけ使い、保存する title は人が書いた形 (前後の空白だけ落とす) のまま。
// まとめたときは最初に置いた形を残す。
func TestAdd_keepsTitleAsWritten(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, testRepo, aside("  Fix  README の  表記 "), source("s1", ""))
	if first.Title != "Fix  README の  表記" {
		t.Errorf("保存した title = %q", first.Title)
	}
	merged := mustAdd(t, s, testRepo, aside("fix readme の表記"), source("s2", ""))
	if merged.ID != first.ID || merged.Title != "Fix  README の  表記" {
		t.Errorf("まとめた後の item = %+v", merged)
	}
	if got, _ := s.list(); len(got) != 1 || got[0].Title != "Fix  README の  表記" {
		t.Errorf("List() = %+v", got)
	}
}

// 大文字と小文字の違いも同じメモとみなす。
func TestAdd_mergeIgnoresCase(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, testRepo, aside("Fix README"), source("s1", ""))
	second := mustAdd(t, s, testRepo, aside("fix readme"), source("s2", ""))
	if first.ID != second.ID {
		t.Errorf("別のメモになった")
	}
}

// repo が違うメモ、open でないメモはまとめない。
func TestAdd_doesNotMergeDifferentItems(t *testing.T) {
	s := New(t.TempDir())
	base := mustAdd(t, s, testRepo, aside("t"), source("s1", ""))
	if other := mustAdd(t, s, "/src/other", aside("t"), source("s1", "")); other.ID == base.ID {
		t.Errorf("repo が違うのにまとめた")
	}
	if _, _, err := s.Set(base.ID, StatusDiscarded, "", ""); err != nil {
		t.Fatal(err)
	}
	if again := mustAdd(t, s, testRepo, aside("t"), source("s1", "")); again.ID == base.ID {
		t.Errorf("discarded のメモにまとめた")
	}
}

// 並行に同じメモを置いても 1 件にまとまる。
func TestAdd_concurrentSameItem(t *testing.T) {
	s := New(t.TempDir())
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if _, err := s.add(testRepo, aside("t"), OriginPlaced, source("s"+string(rune('a'+i)), "")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, _ := s.list()
	if len(got) != 1 || len(got[0].Sources) != 8 {
		t.Errorf("List() = %+v, want 1 件で sources 8 つ", got)
	}
}

func TestAdd_rejectsInvalidEntry(t *testing.T) {
	s := New(t.TempDir())
	cases := map[string]Entry{
		"kind が無い":    {Title: "t"},
		"kind が範囲外":   {Kind: "decision", Title: "t"},
		"kind が task": {Kind: "task", Title: "t"},
		"title が空":    {Kind: KindScope, Title: "  "},
	}
	for name, e := range cases {
		if _, err := s.add(testRepo, e, OriginPlaced, source("s1", "")); err == nil {
			t.Errorf("%s: add() error = nil", name)
		}
	}
	if _, err := s.add(testRepo, aside("t"), OriginPlaced, source("../x", "")); err == nil {
		t.Errorf("ファイル名に使えない session ID を通した")
	}
	if _, err := (Store{}).add(testRepo, aside("t"), OriginPlaced, source("s1", "")); err == nil {
		t.Errorf("置き場所が無いのに通した")
	}
}

func TestSet(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, testRepo, aside("t"), source("s1", ""))

	got, from, err := s.Set(item.ID, StatusHanded, "", ToSession("S2"))
	if err != nil || got.Status != StatusHanded {
		t.Fatalf("Set() = %+v, %v", got, err)
	}
	// 書き換える前の status はログに出す。
	if from != StatusOpen {
		t.Errorf("from = %q, want open", from)
	}
	if list, _ := s.list(); list[0].Status != StatusHanded {
		t.Errorf("書き戻されていない: %+v", list)
	}
	if _, _, err := s.Set(item.ID, "done", "", ""); err == nil {
		t.Errorf("範囲外の status を通した")
	}
	if _, _, err := s.Set("../x", StatusOpen, "", ""); err == nil {
		t.Errorf("ファイル名に使えない id を通した")
	}
}

// from を渡した Set は、ファイルの status が from のときだけ書き換える。2 つの mod が同じ open のメモを渡そうとしても、
// 取れるのは先に書いた 1 つだけ (models/handoff の Claim)。
func TestSet_from(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, testRepo, aside("t"), source("s1", ""))

	if _, _, err := s.Set(item.ID, StatusHanded, StatusOpen, ToSession("S2")); err != nil {
		t.Fatalf("open のメモを from open で取れない: %v", err)
	}
	_, _, err := s.Set(item.ID, StatusHanded, StatusOpen, ToSession("S3"))
	if err == nil {
		t.Fatal("handed のメモを from open で取れた")
	}
	if !strings.Contains(err.Error(), StatusHanded) || !strings.Contains(err.Error(), StatusOpen) {
		t.Errorf("理由に今の status と期待した status が無い: %v", err)
	}
	if _, _, err := s.Set(item.ID, StatusDiscarded, StatusOpen, ""); err == nil {
		t.Error("取られたメモを from open で捨てられた")
	}
	if list, _ := s.list(); list[0].Status != StatusHanded || list[0].HandedTo != "sid:S2" {
		t.Errorf("失敗した Set が書き換えた: %+v", list)
	}
	if _, _, err := s.Set(item.ID, StatusOpen, StatusHanded, ""); err != nil {
		t.Errorf("届かなかったメモを from handed で戻せない: %v", err)
	}
	if _, _, err := s.Set(item.ID, StatusOpen, "done", ""); err == nil {
		t.Error("範囲外の from を通した")
	}
}

// Withdraw は置いたセッションが自分の open のメモを discarded にする。ほかのセッションも置いたメモ、渡した後のメモは
// 書き換えない。
func TestWithdraw(t *testing.T) {
	s := New(t.TempDir())
	own := mustAdd(t, s, testRepo, aside("own"), source("s1", ""))
	shared := mustAdd(t, s, testRepo, aside("shared"), source("s1", ""))
	mustAdd(t, s, testRepo, aside("shared"), source("s2", ""))
	handed := mustAdd(t, s, testRepo, aside("handed"), source("s1", ""))
	if _, _, err := s.Set(handed.ID, StatusHanded, StatusOpen, ToSession("S2")); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Withdraw(own.ID, "s2"); err == nil {
		t.Error("置いていないセッションが取り下げた")
	}
	if _, err := s.Withdraw(shared.ID, "s1"); err == nil {
		t.Error("ほかのセッションも置いたメモを取り下げた")
	}
	// 渡した後のメモは pane の一覧に出ないので、pane ではなく、作ったタスクを閉じるよう案内する。
	if _, err := s.Withdraw(handed.ID, "s1"); err == nil || !strings.Contains(err.Error(), "TaskUpdate") {
		t.Errorf("渡した後のメモを取り下げた、またはタスクを閉じるよう案内しない: %v", err)
	}
	if _, err := s.Withdraw("../x", "s1"); err == nil {
		t.Error("ファイル名に使えない id を通した")
	}
	if _, err := s.Withdraw(own.ID, ""); err == nil {
		t.Error("session ID が空なのに通した")
	}
	got, err := s.Withdraw(own.ID, "s1")
	if err != nil || got.Status != StatusDiscarded {
		t.Fatalf("Withdraw() = %+v, %v", got, err)
	}
	open, _ := s.Find("", StatusOpen)
	if len(open) != 1 || open[0].ID != shared.ID {
		t.Errorf("open = %+v, want shared だけ", open)
	}
	if list, _ := s.Find("", StatusHanded); len(list) != 1 || list[0].HandedTo != "sid:S2" {
		t.Errorf("handed が変わった: %+v", list)
	}
}

// DiscardBefore は最後に置かれた時刻が before より前の open のメモを discarded にし、捨てたメモを返す。
// 置き直されたメモは最後の source の時刻で見る。open でないメモは触らない。
func TestDiscardBefore(t *testing.T) {
	s := New(t.TempDir())
	placed := func(session string, ago time.Duration) Source {
		return Source{SessionID: session, CWD: testRepo, At: testNow.Add(-ago)}
	}
	old := mustAdd(t, s, testRepo, aside("old"), placed("s1", 40*24*time.Hour))
	fresh := mustAdd(t, s, testRepo, aside("fresh"), placed("s1", time.Hour))
	renewed := mustAdd(t, s, testRepo, aside("renewed"), placed("s1", 40*24*time.Hour))
	mustAdd(t, s, testRepo, aside("renewed"), placed("s2", time.Hour))
	handed := mustAdd(t, s, testRepo, aside("handed"), placed("s1", 40*24*time.Hour))
	if _, _, err := s.Set(handed.ID, StatusHanded, "", ToSession("S2")); err != nil {
		t.Fatal(err)
	}

	cutoff := testNow.Add(-30 * 24 * time.Hour)
	got, err := s.DiscardBefore(cutoff)
	if err != nil || len(got) != 1 || got[0].ID != old.ID || got[0].Status != StatusDiscarded {
		t.Fatalf("DiscardBefore() = %+v, %v, want %s だけ discarded", got, err, old.ID)
	}
	open, _ := s.Find("", StatusOpen)
	if len(open) != 2 || open[0].ID != fresh.ID && open[1].ID != fresh.ID || open[0].ID != renewed.ID && open[1].ID != renewed.ID {
		t.Errorf("open = %+v, want fresh と renewed", open)
	}
	if list, _ := s.Find("", StatusHanded); len(list) != 1 || list[0].ID != handed.ID {
		t.Errorf("handed が変わった: %+v", list)
	}
	if again, err := s.DiscardBefore(cutoff); err != nil || again == nil || len(again) != 0 {
		t.Errorf("該当なしは空の配列: %+v, %v", again, err)
	}
	if _, err := (Store{}).DiscardBefore(cutoff); err == nil {
		t.Errorf("置き場所が無いのに通した")
	}
}

func setMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// open は 90 日、handed / discarded は 7 日で消す (mtime で判定する)。
func TestPrune(t *testing.T) {
	s := New(t.TempDir())
	open := mustAdd(t, s, testRepo, aside("open"), source("s1", ""))
	handed := mustAdd(t, s, testRepo, aside("handed"), source("s1", ""))
	if _, _, err := s.Set(handed.ID, StatusHanded, "", ToSession("S2")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	path := func(id string) string { return filepath.Join(s.Dir, id+".json") }

	setMtime(t, path(open.ID), now.Add(-8*24*time.Hour))
	setMtime(t, path(handed.ID), now.Add(-8*24*time.Hour))
	removed, err := s.Prune(now)
	if err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件 (handed だけ)", removed, err)
	}
	if got, _ := s.list(); len(got) != 1 {
		t.Errorf("open が消えた")
	}

	// 同じメモがまた置かれたら mtime が進み、open の 90 日はそこから数え直す (まだ文脈が生きている)。
	setMtime(t, path(open.ID), now.Add(-91*24*time.Hour))
	mustAdd(t, s, testRepo, aside("open"), source("s2", ""))
	if removed, err := s.Prune(now); err != nil || removed != 0 {
		t.Fatalf("Prune() = %d, %v, want 0 件 (置き直されたメモは残す)", removed, err)
	}

	setMtime(t, path(open.ID), now.Add(-91*24*time.Hour))
	if removed, err := s.Prune(now); err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件", removed, err)
	}
}

// Prune は Set と同じロックを取る。Set が読んでから書くまでの間に Prune が消すと、Set の書き込みが消したメモを
// 作り直す (#213 の検討で TLC が出した反例の形)。ロックを持っている間は Prune が消さないことを確かめる。
func TestPrune_waitsForLock(t *testing.T) {
	s := New(t.TempDir())
	memo := mustAdd(t, s, testRepo, aside("handed"), source("s1", ""))
	if _, _, err := s.Set(memo.ID, StatusHanded, "", ToSession("S2")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	path := filepath.Join(s.Dir, memo.ID+".json")
	setMtime(t, path, now.Add(-8*24*time.Hour))

	type pruned struct {
		removed int
		err     error
	}
	done := make(chan pruned, 1)
	var early *pruned
	err := s.locked(func(dir string) error {
		item, ok := readItem(path)
		if !ok {
			return errors.New("メモを読めない")
		}
		go func() {
			removed, err := s.Prune(now)
			done <- pruned{removed, err}
		}()
		select {
		case got := <-done:
			early = &got
		case <-time.After(200 * time.Millisecond):
		}
		return write(dir, item)
	})
	if err != nil {
		t.Fatal(err)
	}
	if early != nil {
		t.Fatalf("Prune がロックを待たずに終わった: %d, %v", early.removed, early.err)
	}
	// ロックを放した後の Prune は、書き直されて mtime の新しいメモを残す。
	if got := <-done; got.err != nil || got.removed != 0 {
		t.Errorf("Prune() = %d, %v, want 0 件 (書き直されたメモは残す)", got.removed, got.err)
	}
}

func TestPrune_nothingYet(t *testing.T) {
	if removed, err := New(t.TempDir()).Prune(time.Now()); err != nil || removed != 0 {
		t.Errorf("Prune() = %d, %v, want 0, nil", removed, err)
	}
}
