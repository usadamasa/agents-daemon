package main

import "github.com/spf13/cobra"

// newRootCmd は agents-daemon の root cobra コマンドを組み立てる。
// テストでも呼べるように関数化してある。
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agents-daemon",
		Short: "Claude Code の利用上限待ちを自動検知し、解除後に再開する",
		Long: "agents-daemon は herdr 越しに Claude Code の pane を監視し、\n" +
			"5 時間ウィンドウの利用上限や一時的なエラーを検知したら、解除後に\n" +
			"自動で再開プロンプトを送るデーモンと CLI を提供する。",
		SilenceUsage: true,
	}
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInspectCmd())
	root.AddCommand(newSimulateCmd())
	return root
}
