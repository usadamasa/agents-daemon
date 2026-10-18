package daemoncli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/board"
	"github.com/usadamasa/agents-daemon/internal/boardlog"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newLogsCmd は `agents-daemon logs` を組み立てる。--board はボードの経路のログ (internal/boardlog) を、
// ボードを開いたセッションの domain (status --board と同じ規則) の分だけ読む。
func newLogsCmd() *cobra.Command {
	var (
		lines   int
		asBoard bool
		viewer  board.Viewer
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "現行ログ (daemon.log、--board ならボードの経路の board-<domain>.log) の末尾を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			if !asBoard {
				return verify.RunLogs(cmd.OutOrStdout(), p.LogFile(), lines)
			}
			if viewer.CWD == "" {
				if viewer.CWD, err = os.Getwd(); err != nil {
					return err
				}
			}
			if viewer.UserHome, err = os.UserHomeDir(); err != nil {
				return err
			}
			return verify.RunLogs(cmd.OutOrStdout(), boardlog.Path(p.StateDir(), board.ViewerDomain(p, viewer)), lines)
		},
	}
	f := cmd.Flags()
	f.IntVarP(&lines, "lines", "n", verify.DefaultLogTailLines, "表示する行数")
	f.BoolVar(&asBoard, "board", false, "ボードの経路 (タスクの記録、パーキング、mod) のログを出す")
	f.StringVar(&viewer.CWD, "cwd", "", "--board: domain を決める cwd (既定は今の cwd)")
	f.StringVar(&viewer.SessionID, "session", "", "--board: セッションの ID。記録が業務の側にあれば業務の分を出す")
	return cmd
}
