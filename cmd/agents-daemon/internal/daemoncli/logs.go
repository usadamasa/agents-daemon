package daemoncli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newLogsCmd は `agents-daemon logs` を組み立てる。
func newLogsCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "現行ログ (daemon.log) の末尾を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return verify.RunLogs(cmd.OutOrStdout(), p, lines)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", verify.DefaultLogTailLines, "表示する行数")
	return cmd
}
