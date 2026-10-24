package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// ErrAlreadyRunning は生存している daemon が既に存在する (lock を他の process が持っている) ことを表す。
var ErrAlreadyRunning = errors.New("daemon は既に起動しています")

// lockFile は起動の排他に使う flock の対象 ($STATE/daemon.lock)。中身は空で、読み書きするのはこのパッケージだけ
// なので apppath には置かない。
func lockFile(p apppath.Paths) string {
	return filepath.Join(p.StateDir(), "daemon.lock")
}

// acquireLock は path を flock(LOCK_EX|LOCK_NB) で取り、開いたままの file を返す。lock は file を Close するか
// process が終わるまで続くので、呼び出し元は daemon の寿命の間 file を手放さない (到達不能になると finalizer が
// Close して lock が外れる)。他の process が持っていれば ErrAlreadyRunning。
//
// pid file を読んで生存を見る判定は「見る」と「起こす」の間に穴があり、daemon が 2 つ起きた (#227)。
// OS の排他なら取れた 1 つだけが進み、持ち主が死ねば OS が解放するので stale も残らない。
func acquireLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lock のディレクトリの作成に失敗: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- 固定パターンから組み立てたパス
	if err != nil {
		return nil, fmt.Errorf("lock file を開けません: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock を取れません: %w", err)
	}
	return f, nil
}
