package main

import "github.com/spf13/cobra"

// newRootCmd は agents-daemon の root cobra コマンドを組み立てる。
// テストでも呼べるように関数化してある。
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agents-daemon",
		Short: "Claude Code のセッションのそばに常駐し、セッションの中からはできないことを背後で担う",
		Long: "agents-daemon は Claude Code のセッション群のそばに 1 プロセスだけ常駐し、\n" +
			"セッションの中からはできないこと (止まったセッションへの働きかけ、セッションを跨いだ\n" +
			"状態の集約、時間で動く掃除、端末多重化ツール rex の block への操作) を背後で担う。\n" +
			"今ある働きかけは、5 時間ウィンドウの利用上限や一時的なエラーで止まったセッションを\n" +
			"解除後に再開させること。",
		SilenceUsage: true,
	}
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInspectCmd())
	root.AddCommand(newSimulateCmd())
	root.AddCommand(newStatuslineCmd())
	root.AddCommand(newIngestStatuslineCmd())
	root.AddCommand(newDreamCmd())
	return root
}
