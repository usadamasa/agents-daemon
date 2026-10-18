package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/board"
	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// statusLogTailLines は `status` が末尾に表示する現行ログの行数。
const statusLogTailLines = 20

// newStatusCmd は `agents-daemon status` を組み立てる。
func newStatusCmd() *cobra.Command {
	var asBoard bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "daemon の稼働状況と監視中の pane 一覧を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			if asBoard {
				return board.Write(cmd.OutOrStdout(), p)
			}
			return printStatus(cmd.OutOrStdout(), p, time.Now())
		},
	}
	cmd.Flags().BoolVar(&asBoard, "board", false, "pane をレーンに分けたボードを JSON で出す (plugin の mod hooks/board.tsx が読む)")
	return cmd
}

// printStatus は daemon の稼働状況、status.json のスナップショット、現行ログの
// 末尾を w へ書く。daemon が起動していない場合はそれを明言して即座に戻る。
func printStatus(w io.Writer, p apppath.Paths, now time.Time) error {
	pid, err := daemon.ReadPIDFile(p.PIDFile())
	if err != nil {
		return err
	}
	if pid == 0 || !daemon.PIDAlive(pid) {
		_, _ = fmt.Fprintln(w, "daemon は起動していません")
		return nil
	}

	_, _ = fmt.Fprintf(w, "daemon 起動中 (pid %d)\n", pid)

	snap, err := daemon.ReadStatusSnapshot(p.StatusFile())
	if err != nil {
		return err
	}
	printSnapshot(w, snap, pid, now)

	tail, err := applog.TailLogLines(p.LogFile(), statusLogTailLines)
	if err != nil {
		_, _ = fmt.Fprintf(w, "ログを読めません: %v\n", err)
		return nil
	}
	if len(tail) > 0 {
		_, _ = fmt.Fprintln(w, "--- ログ (末尾) ---")
		for _, line := range tail {
			_, _ = fmt.Fprintln(w, line)
		}
	}
	return nil
}

// printSnapshot は status.json の内容を書く。
//
// snap.PID を現に動いている pid と突き合わせるのは、前のデーモンが残した
// スナップショットを今のデーモンのものとして見せないため。突き合わせないと
// 「起動: 16 分前」のように、実際には数秒前に起きたデーモンの情報として
// 古い時刻が表示される (実際に踏んだ)。
func printSnapshot(w io.Writer, snap *daemon.StatusSnapshot, pid int, now time.Time) {
	if snap == nil || snap.PID != pid {
		_, _ = fmt.Fprintln(w, "status snapshot がまだありません (起動直後の可能性があります)")
		return
	}
	_, _ = fmt.Fprintf(w, "backend: %s\n", backendColumn(snap.Backend))
	_, _ = fmt.Fprintf(w, "起動: %s (%s 前)\n", snap.StartedAt.Format(time.RFC3339), now.Sub(snap.StartedAt).Round(time.Second))
	_, _ = fmt.Fprintf(w, "最終更新: %s (%s 前)\n", snap.UpdatedAt.Format(time.RFC3339), now.Sub(snap.UpdatedAt).Round(time.Second))
	if len(snap.Panes) == 0 {
		_, _ = fmt.Fprintln(w, "監視中の pane はありません")
		return
	}
	_, _ = fmt.Fprintln(w, "監視中の pane:")
	for _, pn := range snap.Panes {
		wait := "-"
		if pn.WaitUntil != nil {
			wait = pn.WaitUntil.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "  %s  %s  agent=%s%s monitor=%s attempts=%d wait_until=%s last=%s\n",
			pn.PaneID, pn.CWD, pn.AgentStatus, recordColumns(pn), pn.MonitorStatus, pn.Attempts, wait, pn.LastOutcome)
	}
}

// recordColumns は Claude Code の自己申告の kind と msg を、あるときだけ agent= の後ろに足す。
func recordColumns(pn daemon.PaneStatus) string {
	s := ""
	if pn.AgentKind != "" {
		s += " kind=" + pn.AgentKind
	}
	if pn.AgentMessage != "" {
		s += " msg=" + strconv.Quote(pn.AgentMessage)
	}
	return s
}

// backendColumn は backend の表示。記録を始める前の daemon が書いた snapshot では空になる。
func backendColumn(kind string) string {
	if kind == "" {
		return "(記録なし)"
	}
	return kind
}
