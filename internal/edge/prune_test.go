package edge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := testNow.Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// Prune は Retention 更新の無い辺と消し損ねた一時ファイルを消し、新しいものとロックは残す。
func TestPrune(t *testing.T) {
	s := New(t.TempDir())
	old := filepath.Join(s.Dir, "old.json")
	tmp := filepath.Join(s.Dir, ".edge.123")
	fresh := filepath.Join(s.Dir, "fresh.json")
	lock := filepath.Join(s.Dir, lockFile)
	touch(t, old, Retention)
	touch(t, tmp, Retention+time.Hour)
	touch(t, fresh, Retention-time.Hour)
	touch(t, lock, Retention+time.Hour)
	defer func() {
		if _, err := os.Stat(lock); err != nil {
			t.Errorf("ロックが消えた: %v", err)
		}
	}()

	removed, err := s.Prune(testNow)
	if err != nil || removed != 2 {
		t.Fatalf("Prune() = %d, %v, want 2", removed, err)
	}
	for _, p := range []string{old, tmp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s が残った (err=%v)", p, err)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("新しい辺が消えた: %v", err)
	}
}

// 同じ辺を登録し直すと mtime が進み、保持期間を数え直す。
func TestPrune_reAddRefreshes(t *testing.T) {
	s := New(t.TempDir())
	before, after := Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"}
	added := mustAdd(t, s, before, after, "", testNow)
	path := filepath.Join(s.Dir, added.Edge.ID+".json")
	stale := testNow.Add(-Retention)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, before, after, "", testNow)

	if removed, err := s.Prune(testNow.Add(time.Hour)); err != nil || removed != 0 {
		t.Errorf("Prune() = %d, %v, want 0", removed, err)
	}
}

// agedEdge は before → after の辺を書き、mtime を Retention より前にする。ファイルのパスを返す。
func agedEdge(t *testing.T, s Store, before, after string) string {
	t.Helper()
	added := mustAdd(t, s, Endpoint{Ref: before, CWD: "/home/a"}, Endpoint{Ref: after, CWD: "/home/b"}, "", testNow)
	path := filepath.Join(s.Dir, added.Edge.ID+".json")
	stale := testNow.Add(-Retention - time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertModTime(t *testing.T, path string, touched bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.ModTime().Equal(testNow); got != touched {
		t.Errorf("%s の mtime = %v, 進んだか = %v, want %v", path, info.ModTime(), got, touched)
	}
}

// TouchSessions は生きているセッションを端点に持つ辺 (sid: と task:、before と after のどちらでも) の mtime を進める。
func TestTouchSessions(t *testing.T) {
	s := New(t.TempDir())
	sidBefore := agedEdge(t, s, "sid:live", "sid:gone")
	taskAfter := agedEdge(t, s, "sid:gone", "task:live/1")
	unrelated := agedEdge(t, s, "sid:gone", "task:gone/1")
	unbound := agedEdge(t, s, "session:live", "pr:o/r#1")

	if err := s.TouchSessions([]string{"live"}, testNow); err != nil {
		t.Fatalf("TouchSessions() = %v", err)
	}
	assertModTime(t, sidBefore, true)
	assertModTime(t, taskAfter, true)
	assertModTime(t, unrelated, false)
	assertModTime(t, unbound, false)

	if removed, err := s.Prune(testNow); err != nil || removed != 2 {
		t.Errorf("Prune() = %d, %v, want 2 (進めなかった辺だけ)", removed, err)
	}
}

// TouchBefore は before の参照が refs にある辺の mtime を進める。after だけにある辺は進めない。
func TestTouchBefore(t *testing.T) {
	s := New(t.TempDir())
	open := agedEdge(t, s, "pr:o/r#1", "task:a/1")
	afterOnly := agedEdge(t, s, "sid:a", "pr:o/r#1")
	merged := agedEdge(t, s, "pr:o/r#2", "task:a/2")

	if err := s.TouchBefore(map[string]bool{"pr:o/r#1": true}, testNow); err != nil {
		t.Fatalf("TouchBefore() = %v", err)
	}
	assertModTime(t, open, true)
	assertModTime(t, afterOnly, false)
	assertModTime(t, merged, false)
}

func TestTouch_noDir(t *testing.T) {
	missing := New(filepath.Join(t.TempDir(), "missing"))
	for _, s := range []Store{{}, missing} {
		if err := s.TouchSessions([]string{"a"}, testNow); err != nil {
			t.Errorf("TouchSessions() = %v", err)
		}
		if err := s.TouchBefore(map[string]bool{"a": true}, testNow); err != nil {
			t.Errorf("TouchBefore() = %v", err)
		}
	}
	if _, err := os.Stat(missing.Dir); !os.IsNotExist(err) {
		t.Errorf("Touch は edges/ を作らない (err=%v)", err)
	}
}

func TestPrune_noDir(t *testing.T) {
	for _, s := range []Store{{}, New(filepath.Join(t.TempDir(), "missing"))} {
		if removed, err := s.Prune(testNow); err != nil || removed != 0 {
			t.Errorf("Prune() = %d, %v", removed, err)
		}
	}
}
