package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

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

// writePIDFile は path へ pid を原子的に書き込む (statefile)。
func writePIDFile(path string, pid int) error {
	return statefile.WriteFileAtomic(path, ".daemon.pid.*", []byte(strconv.Itoa(pid)+"\n"))
}

// RemovePIDFile は path が存在すれば削除する。既に無い場合はエラーにしない。
func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("PID ファイルの削除に失敗: %w", err)
	}
	return nil
}

// removeOwnPIDFile は path に pid が書いてあるときだけ消す。他の daemon の pid なら残す
// (消すと status / doctor / stop がその daemon を見失う)。無ければ何もしない。
func removeOwnPIDFile(path string, pid int) error {
	current, err := ReadPIDFile(path)
	if err != nil {
		return err
	}
	if current != pid {
		return nil
	}
	return RemovePIDFile(path)
}
