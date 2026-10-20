package parking

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
)

const testRepo = "/src/github.com/usadamasa/agents-daemon"

var testNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func aside(title string) Entry {
	return Entry{Kind: KindAside, Title: title, Detail: "detail", Files: []string{"a.go"}, DefaultTaken: "やらない"}
}

func source(sessionID, taskID string) Source {
	return Source{SessionID: sessionID, TaskID: taskID, CWD: testRepo, At: testNow}
}

func mustAdd(t *testing.T, s Store, d domain.Domain, repo string, e Entry, src Source) Item {
	t.Helper()
	added, err := s.add(d, repo, e, src)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}
	return added.Item
}

// 置いたメモは parking/<domain>/<id>.json に open で書かれ、List で読める。
func TestAdd_writesOneFilePerItem(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, domain.Home, testRepo, aside("保持期間の根拠が無い"), source("s1", "3"))

	if item.Status != StatusOpen || item.Domain != domain.Home || item.Repo != testRepo || item.Kind != KindAside {
		t.Errorf("item = %+v", item)
	}
	if len(item.Sources) != 1 || item.Sources[0].TaskID != "3" {
		t.Errorf("sources = %+v", item.Sources)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "home", item.ID+".json")); err != nil {
		t.Errorf("ファイルが無い: %v", err)
	}
	got, err := s.List(domain.Home)
	if err != nil || len(got) != 1 || got[0].ID != item.ID || got[0].Title != "保持期間の根拠が無い" {
		t.Errorf("List() = %+v, %v", got, err)
	}
	if work, _ := s.List(domain.Work); len(work) != 0 {
		t.Errorf("work の側に出た: %+v", work)
	}
}

// PlacedDuring は session と task の組が sources にあるメモだけを残す。まとめたメモも、後から足した source で引ける。
func TestPlacedDuring(t *testing.T) {
	s := New(t.TempDir())
	during := mustAdd(t, s, domain.Home, testRepo, aside("a"), source("s1", "3"))
	mustAdd(t, s, domain.Home, testRepo, aside("b"), source("s2", "3"))
	merged := mustAdd(t, s, domain.Home, testRepo, aside("b"), source("s1", "3"))
	mustAdd(t, s, domain.Home, testRepo, aside("c"), source("s1", "4"))
	mustAdd(t, s, domain.Home, testRepo, aside("d"), source("s2", "3"))
	mustAdd(t, s, domain.Home, testRepo, aside("e"), source("s1", ""))
	items, _ := s.List(domain.Home)

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

// domain、repo、正規化した title が同じ open のメモには sources を足し、新しいメモを作らない。
func TestAdd_mergesSameItem(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, domain.Home, testRepo, aside("保持期間の  根拠が無い"), source("s1", "3"))
	second := mustAdd(t, s, domain.Home, testRepo, aside(" 保持期間の根拠が無い "), source("s2", "1"))
	if second.ID != first.ID {
		t.Fatalf("別のメモになった: %s != %s", second.ID, first.ID)
	}
	// 同じ置き方 (同じ session と task) を二重に送っても sources は増えない。
	third := mustAdd(t, s, domain.Home, testRepo, aside("保持期間の根拠が無い"), source("s2", "1"))
	if len(third.Sources) != 2 {
		t.Errorf("sources = %+v, want 2 件", third.Sources)
	}
	if got, _ := s.List(domain.Home); len(got) != 1 {
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
		added, err := s.add(domain.Home, testRepo, aside("t"), c.src)
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
	mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s1", ""))
	same := mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s2", ""))
	if same.Sources[1].Detail != "" {
		t.Errorf("同じ detail を sources に残した: %+v", same.Sources[1])
	}
	other := aside("t")
	other.Detail = "別のファイルでも同じ"
	merged := mustAdd(t, s, domain.Home, testRepo, other, source("s3", ""))
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
	mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s1", "3"))
	later := aside("t")
	later.Detail = "後から分かった補足"
	src := source("s1", "3")
	src.At = testNow.Add(time.Hour)
	merged := mustAdd(t, s, domain.Home, testRepo, later, src)
	if len(merged.Sources) != 2 || merged.Sources[1].Detail != "後から分かった補足" || !merged.Sources[1].At.Equal(src.At) {
		t.Fatalf("sources = %+v", merged.Sources)
	}
	again := mustAdd(t, s, domain.Home, testRepo, later, src)
	if len(again.Sources) != 2 {
		t.Errorf("同じ detail の二重送信で増えた: %+v", again.Sources)
	}
	if first := mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s1", "3")); len(first.Sources) != 2 {
		t.Errorf("メモと同じ detail で増えた: %+v", first.Sources)
	}
}

// 正規化した title は同じメモかを見る鍵にだけ使い、保存する title は人が書いた形 (前後の空白だけ落とす) のまま。
// まとめたときは最初に置いた形を残す。
func TestAdd_keepsTitleAsWritten(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, domain.Home, testRepo, aside("  Fix  README の  表記 "), source("s1", ""))
	if first.Title != "Fix  README の  表記" {
		t.Errorf("保存した title = %q", first.Title)
	}
	merged := mustAdd(t, s, domain.Home, testRepo, aside("fix readme の表記"), source("s2", ""))
	if merged.ID != first.ID || merged.Title != "Fix  README の  表記" {
		t.Errorf("まとめた後の item = %+v", merged)
	}
	if got, _ := s.List(domain.Home); len(got) != 1 || got[0].Title != "Fix  README の  表記" {
		t.Errorf("List() = %+v", got)
	}
}

// 大文字と小文字の違いも同じメモとみなす。
func TestAdd_mergeIgnoresCase(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, domain.Home, testRepo, aside("Fix README"), source("s1", ""))
	second := mustAdd(t, s, domain.Home, testRepo, aside("fix readme"), source("s2", ""))
	if first.ID != second.ID {
		t.Errorf("別のメモになった")
	}
}

// repo、domain が違うメモ、open でないメモはまとめない。
func TestAdd_doesNotMergeDifferentItems(t *testing.T) {
	s := New(t.TempDir())
	base := mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s1", ""))
	if other := mustAdd(t, s, domain.Home, "/src/other", aside("t"), source("s1", "")); other.ID == base.ID {
		t.Errorf("repo が違うのにまとめた")
	}
	if other := mustAdd(t, s, domain.Work, testRepo, aside("t"), source("s1", "")); other.ID == base.ID {
		t.Errorf("domain が違うのにまとめた")
	}
	if _, _, err := s.Set(domain.Home, base.ID, StatusDiscarded); err != nil {
		t.Fatal(err)
	}
	if again := mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s1", "")); again.ID == base.ID {
		t.Errorf("discarded のメモにまとめた")
	}
}

// 並行に同じメモを置いても 1 件にまとまる。
func TestAdd_concurrentSameItem(t *testing.T) {
	s := New(t.TempDir())
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if _, err := s.add(domain.Home, testRepo, aside("t"), source("s"+string(rune('a'+i)), "")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, _ := s.List(domain.Home)
	if len(got) != 1 || len(got[0].Sources) != 8 {
		t.Errorf("List() = %+v, want 1 件で sources 8 つ", got)
	}
}

func TestAdd_rejectsInvalidEntry(t *testing.T) {
	s := New(t.TempDir())
	cases := map[string]Entry{
		"kind が無い":  {Title: "t"},
		"kind が範囲外": {Kind: "decision", Title: "t"},
		"title が空":  {Kind: KindScope, Title: "  "},
	}
	for name, e := range cases {
		if _, err := s.add(domain.Home, testRepo, e, source("s1", "")); err == nil {
			t.Errorf("%s: add() error = nil", name)
		}
	}
	if _, err := s.add(domain.Home, testRepo, aside("t"), source("../x", "")); err == nil {
		t.Errorf("ファイル名に使えない session ID を通した")
	}
	if _, err := (Store{}).add(domain.Home, testRepo, aside("t"), source("s1", "")); err == nil {
		t.Errorf("置き場所が無いのに通した")
	}
}

func TestSet(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, domain.Home, testRepo, aside("t"), source("s1", ""))

	got, from, err := s.Set(domain.Home, item.ID, StatusHanded)
	if err != nil || got.Status != StatusHanded {
		t.Fatalf("Set() = %+v, %v", got, err)
	}
	// 書き換える前の status はログに出す。
	if from != StatusOpen {
		t.Errorf("from = %q, want open", from)
	}
	if list, _ := s.List(domain.Home); list[0].Status != StatusHanded {
		t.Errorf("書き戻されていない: %+v", list)
	}
	if _, _, err := s.Set(domain.Home, item.ID, "done"); err == nil {
		t.Errorf("範囲外の status を通した")
	}
	if _, _, err := s.Set(domain.Work, item.ID, StatusOpen); err == nil {
		t.Errorf("別の domain のメモを書き換えた")
	}
	if _, _, err := s.Set(domain.Home, "../x", StatusOpen); err == nil {
		t.Errorf("ファイル名に使えない id を通した")
	}
}

// DiscardBefore は最後に置かれた時刻が before より前の open のメモを discarded にし、捨てたメモを返す。
// 置き直されたメモは最後の source の時刻で見る。open でないメモと別の domain のメモは触らない。
func TestDiscardBefore(t *testing.T) {
	s := New(t.TempDir())
	placed := func(session string, ago time.Duration) Source {
		return Source{SessionID: session, CWD: testRepo, At: testNow.Add(-ago)}
	}
	old := mustAdd(t, s, domain.Home, testRepo, aside("old"), placed("s1", 40*24*time.Hour))
	fresh := mustAdd(t, s, domain.Home, testRepo, aside("fresh"), placed("s1", time.Hour))
	renewed := mustAdd(t, s, domain.Home, testRepo, aside("renewed"), placed("s1", 40*24*time.Hour))
	mustAdd(t, s, domain.Home, testRepo, aside("renewed"), placed("s2", time.Hour))
	handed := mustAdd(t, s, domain.Home, testRepo, aside("handed"), placed("s1", 40*24*time.Hour))
	if _, _, err := s.Set(domain.Home, handed.ID, StatusHanded); err != nil {
		t.Fatal(err)
	}
	work := mustAdd(t, s, domain.Work, testRepo, aside("work"), placed("s1", 40*24*time.Hour))

	cutoff := testNow.Add(-30 * 24 * time.Hour)
	got, err := s.DiscardBefore(domain.Home, cutoff)
	if err != nil || len(got) != 1 || got[0].ID != old.ID || got[0].Status != StatusDiscarded {
		t.Fatalf("DiscardBefore() = %+v, %v, want %s だけ discarded", got, err, old.ID)
	}
	open, _ := s.Find(domain.Home, "", StatusOpen)
	if len(open) != 2 || open[0].ID != fresh.ID && open[1].ID != fresh.ID || open[0].ID != renewed.ID && open[1].ID != renewed.ID {
		t.Errorf("open = %+v, want fresh と renewed", open)
	}
	if list, _ := s.Find(domain.Home, "", StatusHanded); len(list) != 1 || list[0].ID != handed.ID {
		t.Errorf("handed が変わった: %+v", list)
	}
	if list, _ := s.Find(domain.Work, "", StatusOpen); len(list) != 1 || list[0].ID != work.ID {
		t.Errorf("別の domain のメモが変わった: %+v", list)
	}
	if again, err := s.DiscardBefore(domain.Home, cutoff); err != nil || again == nil || len(again) != 0 {
		t.Errorf("該当なしは空の配列: %+v, %v", again, err)
	}
	if _, err := (Store{}).DiscardBefore(domain.Home, cutoff); err == nil {
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
	open := mustAdd(t, s, domain.Home, testRepo, aside("open"), source("s1", ""))
	handed := mustAdd(t, s, domain.Work, testRepo, aside("handed"), source("s1", ""))
	if _, _, err := s.Set(domain.Work, handed.ID, StatusHanded); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	path := func(d domain.Domain, id string) string { return filepath.Join(s.Dir, string(d), id+".json") }

	setMtime(t, path(domain.Home, open.ID), now.Add(-8*24*time.Hour))
	setMtime(t, path(domain.Work, handed.ID), now.Add(-8*24*time.Hour))
	removed, err := s.Prune(now)
	if err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件 (handed だけ)", removed, err)
	}
	if got, _ := s.List(domain.Home); len(got) != 1 {
		t.Errorf("open が消えた")
	}

	// 同じメモがまた置かれたら mtime が進み、open の 90 日はそこから数え直す (まだ文脈が生きている)。
	setMtime(t, path(domain.Home, open.ID), now.Add(-91*24*time.Hour))
	mustAdd(t, s, domain.Home, testRepo, aside("open"), source("s2", ""))
	if removed, err := s.Prune(now); err != nil || removed != 0 {
		t.Fatalf("Prune() = %d, %v, want 0 件 (置き直されたメモは残す)", removed, err)
	}

	setMtime(t, path(domain.Home, open.ID), now.Add(-91*24*time.Hour))
	if removed, err := s.Prune(now); err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件", removed, err)
	}
}

func TestPrune_nothingYet(t *testing.T) {
	if removed, err := New(t.TempDir()).Prune(time.Now()); err != nil || removed != 0 {
		t.Errorf("Prune() = %d, %v, want 0, nil", removed, err)
	}
}
