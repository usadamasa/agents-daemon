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

func TestPrune_noDir(t *testing.T) {
	for _, s := range []Store{{}, New(filepath.Join(t.TempDir(), "missing"))} {
		if removed, err := s.Prune(testNow); err != nil || removed != 0 {
			t.Errorf("Prune() = %d, %v", removed, err)
		}
	}
}
