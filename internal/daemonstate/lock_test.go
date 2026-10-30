package daemonstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 同じ path を別々に open した 2 つの fd は独立した open file description なので、同じ process の中でも
// flock は競合する。別 process を起こさずに排他を確かめられる。
func TestAcquireLock(t *testing.T) {
	t.Run("無いディレクトリでも lock file を作って取れる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "daemon.lock")
		f, err := AcquireLock(path)
		if err != nil {
			t.Fatalf("AcquireLock() error = %v", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("lock file が無い: %v", err)
		}
	})

	t.Run("取られている lock は ErrAlreadyRunning", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.lock")
		first, err := AcquireLock(path)
		if err != nil {
			t.Fatalf("1 つ目の AcquireLock() error = %v", err)
		}
		defer func() { _ = first.Close() }()

		second, err := AcquireLock(path)
		if !errors.Is(err, ErrAlreadyRunning) {
			if second != nil {
				_ = second.Close()
			}
			t.Fatalf("2 つ目の AcquireLock() error = %v, want ErrAlreadyRunning", err)
		}
	})

	t.Run("離した lock は取り直せる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.lock")
		first, err := AcquireLock(path)
		if err != nil {
			t.Fatalf("1 つ目の AcquireLock() error = %v", err)
		}
		if err := first.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		second, err := AcquireLock(path)
		if err != nil {
			t.Fatalf("取り直しの AcquireLock() error = %v", err)
		}
		_ = second.Close()
	})
}
