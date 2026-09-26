package sessionstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrune(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	t.Run("保持期間を過ぎたものだけを消す", func(t *testing.T) {
		s := New(t.TempDir())

		// 利用上限 state は 24 時間｡mktemp の残骸 (statusline が mv の前に殺されると残る)
		// も同じ規則で消える｡
		fresh := filepath.Join(s.RateLimits, "fresh.json")
		stale := filepath.Join(s.RateLimits, "stale.json")
		leftover := filepath.Join(s.RateLimits, ".rate-limits.XXXX")
		writeFile(t, fresh, "{}")
		writeFile(t, stale, "{}")
		writeFile(t, leftover, "{}")
		ageFile(t, stale, 25*time.Hour, now)
		ageFile(t, leftover, 25*time.Hour, now)

		// compact 系は 7 日｡24 時間では消えないことが利用上限との差になる｡
		keep := filepath.Join(s.CompactState, "recent.md")
		drop := filepath.Join(s.Compacted, "old")
		writeFile(t, keep, "")
		writeFile(t, drop, "")
		ageFile(t, keep, 48*time.Hour, now)
		ageFile(t, drop, 8*24*time.Hour, now)

		rateLimits, compacts, err := s.Prune(now)
		if err != nil {
			t.Fatalf("Prune() error = %v", err)
		}
		if rateLimits != 2 {
			t.Errorf("rateLimits = %d, want 2 (古い state と mktemp の残骸)", rateLimits)
		}
		if compacts != 1 {
			t.Errorf("compacts = %d, want 1", compacts)
		}
		for _, path := range []string{fresh, keep} {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("保持期間内の %s が消えている", filepath.Base(path))
			}
		}
		for _, path := range []string{stale, leftover, drop} {
			if _, err := os.Stat(path); err == nil {
				t.Errorf("保持期間を過ぎた %s が残っている", filepath.Base(path))
			}
		}
	})

	t.Run("サブディレクトリは触らない", func(t *testing.T) {
		s := New(t.TempDir())
		sub := filepath.Join(s.RateLimits, "sub")
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		ageFile(t, sub, 25*time.Hour, now)

		if _, _, err := s.Prune(now); err != nil {
			t.Fatalf("Prune() error = %v", err)
		}
		if _, err := os.Stat(sub); err != nil {
			t.Error("サブディレクトリが消えている")
		}
	})

	t.Run("ディレクトリが無ければ何もしない", func(t *testing.T) {
		s := New(t.TempDir())
		rateLimits, compacts, err := s.Prune(now)
		if err != nil || rateLimits != 0 || compacts != 0 {
			t.Errorf("Prune() = %d, %d, %v, want 0, 0, nil", rateLimits, compacts, err)
		}
	})

	t.Run("ゼロ値の Store は何もしない", func(t *testing.T) {
		rateLimits, compacts, err := Store{}.Prune(now)
		if err != nil || rateLimits != 0 || compacts != 0 {
			t.Errorf("Prune() = %d, %d, %v, want 0, 0, nil", rateLimits, compacts, err)
		}
	})
}
