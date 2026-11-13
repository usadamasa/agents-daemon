package edge

import (
	"testing"
)

func mustAnchor(t *testing.T, s Store, sessionID, cwd, pr string) (Edge, bool) {
	t.Helper()
	e, ok, err := s.Anchor(sessionID, cwd, pr, testNow)
	if err != nil {
		t.Fatalf("Anchor() error = %v", err)
	}
	return e, ok
}

func refsOf(t *testing.T, s Store) map[[2]string]Edge {
	t.Helper()
	edges, err := s.Find("", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]string]Edge{}
	for _, e := range edges {
		got[[2]string{e.Before.Ref, e.After.Ref}] = e
	}
	return got
}

// 渡す側が pr → task だけを書いた PR に、PR を出したセッションから PR への辺を導いた印付きで書く (#487)。
// PR の端点の cwd は、PR が出ている辺の端点から写す。note は端点の番号に揃える。
func TestAnchor_PRの上流が無ければ導く(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#92", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "PR #92 のマージの後", testNow)

	e, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#92")
	if !ok {
		t.Fatal("導かなかった")
	}
	if e.Before.Ref != "sid:A" || e.Before.CWD != "/a" || e.Before.Repo != "/a" || e.After.Ref != "pr:o/r#92" || e.After.CWD != "/h" || !e.Derived || e.Note != "PR #92" || !e.CreatedAt.Equal(testNow) {
		t.Errorf("導いた辺 = %+v", e)
	}
	if got, found := refsOf(t, s)[[2]string{"sid:A", "pr:o/r#92"}]; !found || !got.Derived {
		t.Errorf("書いた辺 = %+v (found %v)", got, found)
	}

	// 2 回目は上流があるので書かない。
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#92"); ok {
		t.Error("上流がある PR に導いた")
	}
}

// PR を after に持つ辺 (人が書いた session → pr、結びつく前のものも) があれば導かない。辺に出ていない PR にも導かない。
func TestAnchor_上流があるか辺に無いPRには導かない(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	mustAdd(t, s, Endpoint{Ref: "session:peer", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, "", testNow)

	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); ok {
		t.Error("session → pr がある PR に導いた")
	}
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#2"); ok {
		t.Error("辺に出ていない PR に導いた")
	}
	if got := len(refsOf(t, s)); got != 2 {
		t.Errorf("辺の数 = %d, want 2", got)
	}
}

// セッションが自分の PR のマージを待つ (pr → そのセッションのタスクか sid:) なら導かない。導くと待ちの輪になる
// (models/prlink の NoDaemonLoop)。
func TestAnchor_自分のPRを待つセッションには導かない(t *testing.T) {
	for _, after := range []string{"task:A/2", "sid:A"} {
		s := New(t.TempDir())
		mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/a"}, Endpoint{Ref: after, CWD: "/a"}, "", testNow)
		if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); ok {
			t.Errorf("pr → %s がある A に導いた", after)
		}
	}
}

// 導いた後にセッションが自分の PR を待つ辺を書いたら、導いた辺を消す。人が書いた sid → pr は消さない
// (models/prlink の LoopResolved)。
func TestAnchor_自分のPRを待つ形になったら導いた辺を消す(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#2", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); !ok {
		t.Fatal("導かなかった")
	}
	mustAdd(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#2", CWD: "/h"}, "人が書いた", testNow)

	removed, err := s.Unanchor()
	if err != nil || len(removed) != 0 {
		t.Fatalf("輪が無いのに消した: %+v, %v", removed, err)
	}

	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/a"}, Endpoint{Ref: "task:A/2", CWD: "/a"}, "", testNow)
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#2", CWD: "/a"}, Endpoint{Ref: "task:A/2", CWD: "/a"}, "", testNow)
	removed, err = s.Unanchor()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Before.Ref != "sid:A" || removed[0].After.Ref != "pr:o/r#1" {
		t.Errorf("消した辺 = %+v", removed)
	}
	got := refsOf(t, s)
	if _, found := got[[2]string{"sid:A", "pr:o/r#1"}]; found {
		t.Error("導いた辺が残っている")
	}
	if _, found := got[[2]string{"sid:A", "pr:o/r#2"}]; !found {
		t.Error("人が書いた辺を消した")
	}
}

// 導いた辺を人が同じ参照の組で書き直すと、人の辺になる (印が消え、Unanchor の対象から外れる)。
func TestAnchor_人が書き直すと導いた印が消える(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); !ok {
		t.Fatal("導かなかった")
	}
	added := mustAdd(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, "PR #1", testNow)
	if added.Created || added.Edge.Derived {
		t.Errorf("書き直した辺 = %+v", added)
	}
}

// 参照の形が違うものは導かない (エラー)。
func TestAnchor_不正な参照(t *testing.T) {
	s := New(t.TempDir())
	for _, c := range []struct{ sessionID, cwd, pr string }{
		{"", "/a", "pr:o/r#1"},
		{"A", "a", "pr:o/r#1"},
		{"A", "/a", "issue:o/r#1"},
		{"A", "/a", "pr:o/r"},
	} {
		if _, _, err := s.Anchor(c.sessionID, c.cwd, c.pr, testNow); err == nil {
			t.Errorf("Anchor(%q, %q, %q) にエラーが無い", c.sessionID, c.cwd, c.pr)
		}
	}
}
