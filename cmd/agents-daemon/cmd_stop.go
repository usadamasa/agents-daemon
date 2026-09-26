package main

import (
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// stopTimeout は SIGTERM 送信後、daemon の終了を待つ最大時間。
const stopTimeout = 10 * time.Second

// newStopCmd は `agents-daemon stop` を組み立てる。
func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "daemon を停止する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return stopDaemon(cmd.OutOrStdout(), p, stopTimeout)
		},
	}
}

// stopDaemon は PID ファイルの daemon へ SIGTERM を送り、終了を確認してから
// 戻る。daemon 自身の SIGTERM ハンドラが正常系では PID ファイルを削除するが、
// 既に死んでいるプロセスの stale な PID ファイルはここで片付ける。
func stopDaemon(w io.Writer, p apppath.Paths, timeout time.Duration) error {
	pid, err := daemon.ReadPIDFile(p.PIDFile())
	if err != nil {
		return err
	}
	if pid == 0 || !daemon.PIDAlive(pid) {
		if err := daemon.RemovePIDFile(p.PIDFile()); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, "daemon は起動していません")
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("daemon (pid %d) への SIGTERM 送信に失敗: %w\n"+
			"Claude Code のセッション内 (sandbox) から実行するとシグナル送信がブロックされることがあります。"+
			"通常のターミナルから実行するか、daemon のアイドル自動終了を待ってください。", pid, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !daemon.PIDAlive(pid) {
			_, _ = fmt.Fprintf(w, "daemon (pid %d) を停止しました\n", pid)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("daemon (pid %d) が %s 以内に終了しませんでした", pid, timeout)
}
