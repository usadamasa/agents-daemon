package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrAlreadyRunning は生存している daemon が既に存在することを表す。
var ErrAlreadyRunning = errors.New("daemon は既に起動しています")

// ReadPIDFile は path から PID を読む。ファイルが存在しない場合や中身が
// 数値として読めない場合は (0, nil) を返す (どちらも「生存プロセス無し」として
// 扱えば十分で、reclaim 可能な状態だから)。
func ReadPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が固定パスを渡す
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("PID ファイルの読み込みに失敗: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, nil
	}
	return pid, nil
}

// PIDAlive は pid のプロセスが生存しているかを判定する。
//
// ps は使わない。sandbox 下の Claude Code セッションからは /bin/ps が
// Operation not permitted で起動できず、全 PID を「不在」と誤判定する既知の
// 問題があるため。代わりに
// syscall.Kill(pid, 0) 相当のシグナル 0 送信で判定する。EPERM (別ユーザーの
// 生存プロセスで権限が無い) も生存として扱う。
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// WritePIDFile は path へ pid を原子的に書き込む (同ディレクトリの一時ファイルに
// 書いて rename)。
func writePIDFile(path string, pid int) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("状態ディレクトリの作成に失敗: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".daemon.pid.*")
	if err != nil {
		return fmt.Errorf("PID ファイルの一時ファイル作成に失敗: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("PID の書き込みに失敗: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("PID ファイルのクローズに失敗: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("PID ファイルの rename に失敗: %w", err)
	}
	return nil
}

// RemovePIDFile は path が存在すれば削除する。既に無い場合はエラーにしない。
func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("PID ファイルの削除に失敗: %w", err)
	}
	return nil
}

// CheckRunning は PID ファイルを見て、生存している daemon が既にあるかを判定する。
// 生存していれば ErrAlreadyRunning を返す。ファイルが無い、または中身の PID が
// 生存していない (stale) 場合は nil を返す — stale なファイルの reclaim (上書き) は
// 呼び出し元が WritePIDFile で行う。
func checkRunning(path string) error {
	pid, err := ReadPIDFile(path)
	if err != nil {
		return err
	}
	if pid != 0 && PIDAlive(pid) {
		return ErrAlreadyRunning
	}
	return nil
}
