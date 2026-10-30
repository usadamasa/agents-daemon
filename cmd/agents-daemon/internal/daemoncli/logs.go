package daemoncli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/runtime/verify"
)

// newLogsCmd は `agents-daemon logs` を組み立てる。--events は出来事のログ (internal/eventlog) を読む。
func newLogsCmd() *cobra.Command {
	var (
		lines    int
		asEvents bool
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "現行ログ (daemon.log、--events なら出来事の events.log) の末尾を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			path := p.LogFile()
			if asEvents {
				path = eventlog.Path(p.StateDir())
			}
			return verify.RunLogs(cmd.OutOrStdout(), path, lines)
		},
	}
	f := cmd.Flags()
	f.IntVarP(&lines, "lines", "n", verify.DefaultLogTailLines, "表示する行数")
	f.BoolVar(&asEvents, "events", false, "依存図とパーキングロットの経路 (タスクの記録、パーキング、mod) の出来事のログを出す")
	return cmd
}
