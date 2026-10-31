package daemonstate

import (
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// StopTimeout は SIGTERM 送信後、daemon の終了を待つ最大時間。
const StopTimeout = 10 * time.Second

// StopResult は Stop が止めた daemon。起動していなければ WasRunning は false。`stop --json` はこれをそのまま出す。
type StopResult struct {
	WasRunning bool `json:"was_running"`
	PID        int  `json:"pid,omitempty"`
}

// Stop は PID ファイルの daemon へ SIGTERM を送り、終了を確認してから
// 戻る。daemon 自身の SIGTERM ハンドラが正常系では PID ファイルを削除するが、
// 既に死んでいるプロセスの stale な PID ファイルはここで片付ける。
func Stop(p apppath.Paths, timeout time.Duration) (StopResult, error) {
	pid, err := ReadPIDFile(p.PIDFile())
	if err != nil {
		return StopResult{}, err
	}
	if pid == 0 || !PIDAlive(pid) {
		return StopResult{}, removePIDFile(p.PIDFile())
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return StopResult{}, fmt.Errorf("daemon (pid %d) への SIGTERM 送信に失敗: %w\n"+
			"Claude Code のセッション内 (sandbox) から実行するとシグナル送信がブロックされることがあります。"+
			"通常のターミナルから実行するか、daemon のアイドル自動終了を待ってください。", pid, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !PIDAlive(pid) {
			return StopResult{WasRunning: true, PID: pid}, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return StopResult{}, fmt.Errorf("daemon (pid %d) が %s 以内に終了しませんでした", pid, timeout)
}

// WriteStop は Stop の結果を人が読む 1 行で w に書く。
func WriteStop(w io.Writer, res StopResult) {
	if !res.WasRunning {
		_, _ = fmt.Fprintln(w, "daemon は起動していません")
		return
	}
	_, _ = fmt.Fprintf(w, "daemon (pid %d) を停止しました\n", res.PID)
}
