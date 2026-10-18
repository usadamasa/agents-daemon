package harvest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// detachLogName は Stop hook が起こした harvest の stderr を受けるログの名前。daemon.log と同じ logs/ に置く。
const detachLogName = "dream-harvest.log"

// DetachFlag は harvest を Stop hook から起こすときの flag。親はこれを外した引数で自分を detach 起動する。
const DetachFlag = "--detach"

// DetachLogPath は logs ディレクトリ (apppath.Paths.LogFile の親) から detach した harvest のログの場所を導出する。
func DetachLogPath(logDir string) string {
	return filepath.Join(logDir, detachLogName)
}

// Detach は自分の実行ファイルを、args から DetachFlag を外した引数で detach 起動して戻る。
// 子の stdout / stderr は logDir の DetachLogPath に残る。
func Detach(args []string, logDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("実行ファイルパスの取得に失敗: %w", err)
	}
	return SpawnDetached(exe, childArgs(args), DetachLogPath(logDir))
}

// childArgs は detach 起動する子に渡す引数。自分の引数から detach の flag だけを外す。
func childArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == DetachFlag {
			continue
		}
		out = append(out, a)
	}
	return out
}

// SpawnDetached は exe を args で detach 起動し、stdout / stderr を logPath に追記する。
// Stop hook から呼ばれる想定で、子を Wait せずに即座に戻る (hook をブロックしない)。
// stdin は繋がない。繋ぐと呼び出し元が子の終了までパイプを待ち続ける。
// 成功した harvest は何も書かないので、ログが伸びるのは失敗したときだけ。
func SpawnDetached(exe string, args []string, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("ログディレクトリの作成に失敗: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- 固定の名前から組み立てたパス
	if err != nil {
		return fmt.Errorf("ログファイルを開けません: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, args...) // #nosec G204 -- 呼び出し元が自分自身の実行ファイルパスを渡す
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("harvest の起動に失敗: %w", err)
	}
	// Wait しない (detach させるため)。Release は Wait を呼ばない場合の後始末。
	return cmd.Process.Release()
}
