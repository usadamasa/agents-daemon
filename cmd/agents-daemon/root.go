package main

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/daemoncli"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/depscli"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/dreamcli"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/parkcli"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statuslinecli"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/worktreecli"
)

// version は建てた元の plugin の version。hooks/build-daemon.sh が -ldflags -X main.version=<version> で埋め込む。
// stamp (agents-daemon.version) は cache の外から読めないことがあるので、バイナリ自身にも名乗らせる。
var version = "dev"

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
		// error は main が 1 行だけ出す。cobra にも出させると、detach 起動した daemon の stderr (daemon.log) に
		// 同じ文が 2 行残る。
		SilenceErrors: true,
		Version:       version,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "建てた元の plugin の version を表示する",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, _ []string) { cmd.Println(version) },
	})
	// グローバルオプションは --json だけ。対応はコマンドが Annotations で宣言する (internal/jsonflag)。
	jsonflag.Add(root)
	// コマンドの組み立ては機能ごとの internal/<family>cli が持ち、ここは登録だけにする。
	for _, family := range [][]*cobra.Command{
		daemoncli.Commands(), statuslinecli.Commands(), depscli.Commands(), parkcli.Commands(), dreamcli.Commands(), worktreecli.Commands(),
	} {
		root.AddCommand(family...)
	}
	return root
}
