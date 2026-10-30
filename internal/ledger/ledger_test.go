package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

// testStore は repo を git で求めず、cwd の末尾を足した鍵にし、求めた回数を数える。
func testStore(t *testing.T) (Store, *int) {
	t.Helper()
	calls := 0
	s := New(t.TempDir())
	s.repoKey = func(cwd string) string {
		calls++
		return cwd + "#repo"
	}
	return s, &calls
}

func byID(t *testing.T, s Store) map[string]Session {
	t.Helper()
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Session{}
	for _, x := range list {
		m[x.SessionID] = x
	}
	return m
}

// Observe は見えたセッションを sessions/<id>.json に書く。first_seen は最初に見た時刻のまま動かず、last_seen は
// 見るたびに進む。名前は後から付いても埋め、見えなくなった名前では消さない。repo は cwd が変わったときだけ求め直す。
func TestObserve(t *testing.T) {
	s, calls := testStore(t)
	if err := s.Observe([]Seen{{SessionID: "A", CWD: "/w/a"}, {SessionID: "B", CWD: "/w/b", Name: "peer"}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe([]Seen{{SessionID: "A", CWD: "/w/a", Name: "writer"}}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe([]Seen{{SessionID: "A", CWD: "/w/a2"}, {SessionID: "B", CWD: "/w/b"}}, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := byID(t, s)
	a, b := got["A"], got["B"]
	if !a.FirstSeen.Equal(t0) || !a.LastSeen.Equal(t0.Add(2*time.Minute)) || a.Name != "writer" || a.CWD != "/w/a2" || a.Repo != "/w/a2#repo" {
		t.Errorf("A = %+v", a)
	}
	if !b.FirstSeen.Equal(t0) || !b.LastSeen.Equal(t0.Add(2*time.Minute)) || b.Name != "peer" || b.Repo != "/w/b#repo" {
		t.Errorf("B = %+v", b)
	}
	if *calls != 3 {
		t.Errorf("repo を求めた回数 = %d, want 3 (A の 2 つの cwd と B)", *calls)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "A.json")); err != nil {
		t.Errorf("sessions/A.json が無い: %v", err)
	}
}

// ファイル名に使えない session ID は書かない。
func TestObserve_invalidID(t *testing.T) {
	s, _ := testStore(t)
	if err := s.Observe([]Seen{{SessionID: "../x", CWD: "/w"}, {SessionID: "", CWD: "/w"}}, t0); err == nil {
		t.Errorf("Observe が通った")
	}
	if got := byID(t, s); len(got) != 0 {
		t.Errorf("書いた: %+v", got)
	}
}

// Pick の候補は、名前が一致し、辺を書いた時刻に生きていたか後から現れた (last_seen が created_at 以後の) セッション。
// 辺を書いた時点で動いていた候補があれば first_seen の最も新しいもの (直前に起こした相手)、無ければ後から最初に
// 現れたものを選ぶ。辺を書く前に終わった同名のセッションは選ばない。
func TestPick(t *testing.T) {
	created := t0.Add(time.Hour)
	exited := Session{SessionID: "exited", Name: "peer", FirstSeen: t0, LastSeen: created.Add(-time.Second)}
	older := Session{SessionID: "older", Name: "peer", FirstSeen: t0.Add(time.Minute), LastSeen: created.Add(time.Hour)}
	newer := Session{SessionID: "newer", Name: "peer", FirstSeen: created.Add(-10 * time.Second), LastSeen: created.Add(time.Minute)}
	first := Session{SessionID: "first", Name: "peer", FirstSeen: created.Add(time.Minute), LastSeen: created.Add(2 * time.Hour)}
	second := Session{SessionID: "second", Name: "peer", FirstSeen: created.Add(time.Hour), LastSeen: created.Add(3 * time.Hour)}
	other := Session{SessionID: "other", Name: "x", FirstSeen: created.Add(-time.Second), LastSeen: created.Add(time.Hour)}
	for _, c := range []struct {
		name     string
		sessions []Session
		want     string
	}{
		{"古い同名がまだ生きていても、直前に起こした新しい方", []Session{exited, older, newer, first, other}, "newer"},
		{"辺を書いた時点で動いていたのが 1 つ", []Session{exited, older, first}, "older"},
		{"動いていたものが無ければ後から最初に現れたもの", []Session{exited, second, first, other}, "first"},
		{"辺を書いた時刻ちょうどに現れたものは動いていた側", []Session{{SessionID: "at", Name: "peer", FirstSeen: created, LastSeen: created}, first}, "at"},
	} {
		if id, ok := Pick(c.sessions, "peer", created); !ok || id != c.want {
			t.Errorf("%s: Pick = %q, %v, want %s", c.name, id, ok, c.want)
		}
	}
	if id, ok := Pick([]Session{exited, other}, "peer", created); ok {
		t.Errorf("辺を書く前に終わったセッションを選んだ: %q", id)
	}
	if _, ok := Pick([]Session{older}, "nobody", created); ok {
		t.Errorf("名前の違うセッションを選んだ")
	}
	// first_seen が同じなら session ID の順。
	for _, at := range []time.Time{t0, created.Add(time.Minute)} {
		tie := []Session{
			{SessionID: "b", Name: "peer", FirstSeen: at, LastSeen: created.Add(time.Hour)},
			{SessionID: "a", Name: "peer", FirstSeen: at, LastSeen: created.Add(time.Hour)},
		}
		if id, _ := Pick(tie, "peer", created); id != "a" {
			t.Errorf("同時刻 %v: Pick = %q, want a", at, id)
		}
	}
}

// Prune は Retention のあいだ見えなかったセッションを mtime で消す。
func TestPrune(t *testing.T) {
	s, _ := testStore(t)
	if err := s.Observe([]Seen{{SessionID: "old", CWD: "/w"}, {SessionID: "fresh", CWD: "/w"}}, t0); err != nil {
		t.Fatal(err)
	}
	stale := t0.Add(-Retention)
	if err := os.Chtimes(filepath.Join(s.Dir, "old.json"), stale, stale); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(t0); err != nil || n != 1 {
		t.Fatalf("Prune() = %d, %v", n, err)
	}
	if got := byID(t, s); len(got) != 1 || got["fresh"].SessionID == "" {
		t.Errorf("残り = %+v", got)
	}
	for _, z := range []Store{{}, New(filepath.Join(t.TempDir(), "missing"))} {
		if n, err := z.Prune(t0); err != nil || n != 0 {
			t.Errorf("置き場の無い Prune() = %d, %v", n, err)
		}
		if list, err := z.List(); err != nil || len(list) != 0 {
			t.Errorf("置き場の無い List() = %+v, %v", list, err)
		}
	}
}
