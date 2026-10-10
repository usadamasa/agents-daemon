package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/hookreport"
)

// herdrTitleTimeout は herdr との全往復と git を合わせた上限｡利用者の hook timeout (5 秒) の内側に収める｡
const herdrTitleTimeout = 4 * time.Second

// newHerdrTitleCmd は `agents-daemon herdr-title` を組み立てる｡
//
// UserPromptSubmit / Stop hook (hooks/herdr-title.sh) が stdin をそのまま渡してくる｡hook の入力は使わないが､
// 読み切らないと Claude Code が閉じたパイプへ書くことになるので捨てる｡失敗は exit 0 で返し､理由を stderr に
// 1 行出す｡stdout は UserPromptSubmit では context に入るので何も出さない｡
func newHerdrTitleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "herdr-title",
		Short: "Claude Code のセッション名とブランチを herdr の space 欄とタブ行へ写す",
		Long: "HERDR_SOCKET_PATH の socket を直接叩き､pane.current で得たタブと workspace に対して\n" +
			"端末タイトル (Claude Code が置くセッションの要約) を token `claude` とタブの label に､\n" +
			"foreground_cwd のブランチを token `branch` に報告する｡herdr の pane の外では何もしない｡\n" +
			"失敗しても exit 0 (理由は stderr)｡",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = io.Copy(io.Discard, cmd.InOrStdin())
			ctx, cancel := context.WithTimeout(cmd.Context(), herdrTitleTimeout)
			defer cancel()
			if _, err := hookreport.Title(ctx, hookreport.DefaultTitleDeps()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "herdr-title:", err)
			}
			return nil
		},
	}
}
