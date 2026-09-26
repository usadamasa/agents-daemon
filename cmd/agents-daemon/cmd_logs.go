package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// defaultLogTailLines は `logs` が既定で表示する行数｡
const defaultLogTailLines = 80

// newLogsCmd は `agents-daemon logs` を組み立てる｡
func newLogsCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "現行ログ (daemon.log) の末尾を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines <= 0 {
				return fmt.Errorf("-n には 1 以上を指定してください (指定値: %d)", lines)
			}
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return printLogs(cmd.OutOrStdout(), p, lines)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", defaultLogTailLines, "表示する行数")
	return cmd
}

// printLogs は現行ログの末尾 n 行を w へ書く｡ログが無いこと自体は異常では
// ないので (daemon 未起動､あるいは報告すべき出来事が何も起きていない)､
// エラーにせずその旨を伝える｡
func printLogs(w io.Writer, p apppath.Paths, n int) error {
	tail, err := applog.TailLogLines(p.LogFile(), n)
	if err != nil {
		return err
	}
	if len(tail) == 0 {
		_, _ = fmt.Fprintln(w, "ログはまだありません")
		return nil
	}
	for _, line := range tail {
		_, _ = fmt.Fprintln(w, line)
	}
	return nil
}
