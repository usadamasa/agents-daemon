package daemoncli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/boardlog"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newLogsCmd は `agents-daemon logs` を組み立てる。--board はボードの経路のログ (internal/boardlog) を読む。
func newLogsCmd() *cobra.Command {
	var (
		lines   int
		asBoard bool
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "現行ログ (daemon.log、--board ならボードの経路の board.log) の末尾を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			path := p.LogFile()
			if asBoard {
				path = boardlog.Path(p.StateDir())
			}
			return verify.RunLogs(cmd.OutOrStdout(), path, lines)
		},
	}
	f := cmd.Flags()
	f.IntVarP(&lines, "lines", "n", verify.DefaultLogTailLines, "表示する行数")
	f.BoolVar(&asBoard, "board", false, "ボードの経路 (タスクの記録、パーキング、mod) のログを出す")
	return cmd
}
