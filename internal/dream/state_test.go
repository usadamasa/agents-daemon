package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDir_UnderStateDir(t *testing.T) {
	if got, want := Dir("/xdg/state/agents-daemon"), "/xdg/state/agents-daemon/dream"; got != want {
		t.Errorf("Dir() = %q; want %q", got, want)
	}
}

func TestResolveDate_explicit(t *testing.T) {
	if got := ResolveDate("2020-01-01"); got != "2020-01-01" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveDate_default(t *testing.T) {
	got := ResolveDate("")
	if len(got) != len("2006-01-02") {
		t.Fatalf("unexpected date format: %q", got)
	}
}

// writeAged は dir/name を作り、mtime を now から age だけ戻す。
func writeAged(t *testing.T, dir, name string, now time.Time, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := now.Add(-age)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrune_RemovesOnlyExpiredFilesInEachDir(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()

	old := writeAged(t, filepath.Join(root, "harvest"), "2026-06-01.jsonl", now, retention+time.Hour)
	fresh := writeAged(t, filepath.Join(root, "harvest"), "2026-10-17.jsonl", now, time.Hour)
	oldCand := writeAged(t, filepath.Join(root, "candidates"), "p-2026-06-01.jsonl", now, retention+time.Hour)
	oldReplay := writeAged(t, filepath.Join(root, "replays"), "c-001.json", now, retention+time.Hour)
	oldVerdict := writeAged(t, filepath.Join(root, "verdicts"), "c-001.json", now, retention+time.Hour)
	// 直下の harvest-state.json と、対象外のサブディレクトリは古くても触らない。
	state := writeAged(t, root, "harvest-state.json", now, retention+time.Hour)
	other := writeAged(t, filepath.Join(root, "other"), "x.json", now, retention+time.Hour)

	removed, err := Prune(root, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 4 {
		t.Errorf("removed = %d; want 4", removed)
	}
	for _, p := range []string{old, oldCand, oldReplay, oldVerdict} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s が消えていない", p)
		}
	}
	for _, p := range []string{fresh, state, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s が消えた: %v", p, err)
		}
	}
}

func TestPrune_MissingDirsAreNotAnError(t *testing.T) {
	removed, err := Prune(filepath.Join(t.TempDir(), "nope"), time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d; want 0", removed)
	}
}
