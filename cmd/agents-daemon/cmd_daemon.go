package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// newDaemonCmd は `agents-daemon daemon` を組み立てる｡
func newDaemonCmd() *cobra.Command {
	var ensure bool
	var foreground bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "監視デーモンを起動する",
		Long: "--ensure は SessionStart hook から呼ばれる想定で､生存している daemon が\n" +
			"既にあれば即座に戻り (exit 0)､無ければこのバイナリを detach 起動して戻る｡\n" +
			"hook を絶対にブロックしないための挙動｡\n" +
			"--foreground はこのプロセス自身でループを回す (デバッグ・テスト用)｡\n" +
			"どちらか一方を必ず指定する｡",
		RunE: func(cmd *cobra.Command, args []string) error {
			if ensure == foreground {
				return errors.New("--ensure か --foreground のどちらか一方を指定してください")
			}

			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}

			if foreground {
				return daemon.RunForeground(cmd.Context(), p, dryRun)
			}
			return daemon.EnsureDaemon(p, dryRun)
		},
	}
	cmd.Flags().BoolVar(&ensure, "ensure", false, "生存している daemon が無ければ detach 起動する")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "このプロセス自身でループを回す (デバッグ用)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "検知と判定は行うが､pane への送信とラベル更新をしない")
	return cmd
}
