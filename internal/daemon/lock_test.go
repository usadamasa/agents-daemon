package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// 同じ path を別々に open した 2 つの fd は独立した open file description なので、同じ process の中でも
// flock は競合する。別 process を起こさずに排他を確かめられる。
func TestAcquireLock(t *testing.T) {
	t.Run("無いディレクトリでも lock file を作って取れる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "daemon.lock")
		f, err := acquireLock(path)
		if err != nil {
			t.Fatalf("acquireLock() error = %v", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("lock file が無い: %v", err)
		}
	})

	t.Run("取られている lock は ErrAlreadyRunning", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.lock")
		first, err := acquireLock(path)
		if err != nil {
			t.Fatalf("1 つ目の acquireLock() error = %v", err)
		}
		defer func() { _ = first.Close() }()

		second, err := acquireLock(path)
		if !errors.Is(err, ErrAlreadyRunning) {
			if second != nil {
				_ = second.Close()
			}
			t.Fatalf("2 つ目の acquireLock() error = %v, want ErrAlreadyRunning", err)
		}
	})

	t.Run("離した lock は取り直せる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.lock")
		first, err := acquireLock(path)
		if err != nil {
			t.Fatalf("1 つ目の acquireLock() error = %v", err)
		}
		if err := first.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		second, err := acquireLock(path)
		if err != nil {
			t.Fatalf("取り直しの acquireLock() error = %v", err)
		}
		_ = second.Close()
	})
}

// lockedPaths は lock を取った状態の Paths を返す。lock は test の終わりに離す。
func lockedPaths(t *testing.T) apppath.Paths {
	t.Helper()
	p := apppath.New(t.TempDir(), "", "")
	f, err := acquireLock(lockFile(p))
	if err != nil {
		t.Fatalf("セットアップの acquireLock() error = %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return p
}

func TestEnsureDaemon_LockHeldDoesNotSpawn(t *testing.T) {
	p := lockedPaths(t)
	spawned := 0
	err := ensureDaemon(p, func() error {
		spawned++
		return nil
	})
	if err != nil {
		t.Fatalf("ensureDaemon() error = %v, want nil (動いている daemon があれば何もしない)", err)
	}
	if spawned != 0 {
		t.Errorf("spawn が %d 回呼ばれた, want 0", spawned)
	}
}

func TestEnsureDaemon_LockFreeSpawnsAndReleases(t *testing.T) {
	p := apppath.New(t.TempDir(), "", "")
	spawned := 0
	err := ensureDaemon(p, func() error {
		spawned++
		// 子はこの後で lock を取り直す。親が握ったままだと子は ErrAlreadyRunning で終わり、daemon が 0 になる。
		f, err := acquireLock(lockFile(p))
		if err != nil {
			return err
		}
		return f.Close()
	})
	if err != nil {
		t.Fatalf("ensureDaemon() error = %v", err)
	}
	if spawned != 1 {
		t.Errorf("spawn が %d 回呼ばれた, want 1", spawned)
	}
}

func TestRunForeground_LockHeldReturnsErrAlreadyRunning(t *testing.T) {
	p := lockedPaths(t)
	// lock の判定は設定の読み込みや rex への到達確認より前。REX_SESSION が無くてもここで戻る。
	err := RunForeground(context.Background(), p, false)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("RunForeground() error = %v, want ErrAlreadyRunning", err)
	}
	if _, statErr := os.Stat(p.PIDFile()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("lock を取れなかった側が pid file を書いた: err = %v", statErr)
	}
}
