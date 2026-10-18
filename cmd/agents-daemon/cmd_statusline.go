package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/statusline"
)

// newStatuslineCmd は `agents-daemon statusline` を組み立てる。
//
// settings の statusLine.command から描画のたびに呼ばれ、stdin の JSON を受けて描画を stdout に出す。
// 同じプロセスで ingest-statusline と同じ state file (context/ と rate-limits/) と cost の記録も
// 書くので、描画ごとの fork は 1 つで済む。失敗しても nil を返して exit 0 にするのは statusline が
// fail-silent でなければならないため (非 0 は status バーを汚す)。理由は stderr に出す。
func newStatuslineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Claude Code の statusline を描画し、同じプロセスで daemon が読む state file を書く",
		Long: "Claude Code が statusline コマンドへ渡す JSON を stdin で受け取り、3 行 (幅が足りなければ 1 行、\n" +
			"さらに狭ければ何も出さない) の statusline を stdout に出す。描画の前に ingest-statusline と同じ\n" +
			"state file を書き、" + statusline.SpendCapEnv + " があれば cost の合算も記録する。\n" +
			"settings.json には次のように書く:\n\n" +
			`  "statusLine": {"type": "command", "command": "${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon statusline", "padding": 0}` + "\n\n" +
			"失敗しても exit 0 で終わる (理由は stderr)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "statusline:", err)
				return nil
			}
			out, err := statusline.Run(cmd.InOrStdin(), p, os.Getenv, time.Now())
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "statusline:", err)
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
}
