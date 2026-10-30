package daemon

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
)

// lockedPaths は lock を取った状態の Paths を返す。lock は test の終わりに離す。
func lockedPaths(t *testing.T) apppath.Paths {
	t.Helper()
	p := apppath.New(t.TempDir(), "", "")
	f, err := daemonstate.AcquireLock(daemonstate.LockFile(p))
	if err != nil {
		t.Fatalf("セットアップの AcquireLock() error = %v", err)
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
		f, err := daemonstate.AcquireLock(daemonstate.LockFile(p))
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
	if !errors.Is(err, daemonstate.ErrAlreadyRunning) {
		t.Fatalf("RunForeground() error = %v, want ErrAlreadyRunning", err)
	}
	if _, statErr := os.Stat(p.PIDFile()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("lock を取れなかった側が pid file を書いた: err = %v", statErr)
	}
}
