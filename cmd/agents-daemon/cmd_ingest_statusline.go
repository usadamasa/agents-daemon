package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// newIngestStatuslineCmd は `agents-daemon ingest-statusline` を組み立てる｡
//
// statusline script が描画のたびに stdin をそのまま渡してくる｡失敗しても nil を返して
// exit 0 にするのは､statusline は fail-silent でなければならず (非 0 や stdout への
// 出力は status バーを汚す)､呼び出し側の 1 行 (`... ingest-statusline 2>/dev/null || :`)
// も失敗を捨てる前提だから｡理由は stderr に 1 行だけ出し､手で実行したときに追える
// ようにする｡daemon のログには書かない (daemon が動いているかに依存しない)｡
func newIngestStatuslineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ingest-statusline",
		Short: "statusline の stdin から context/ と rate-limits/ の state file を書く",
		Long: "Claude Code が statusline コマンドへ渡す JSON を stdin で受け取り､daemon が読む\n" +
			"context/<session_id>.json (毎回) と rate-limits/<session_id>.json\n" +
			"(rate_limits.five_hour.resets_at があるときだけ) を書く｡\n" +
			"statusline script からは次の 1 行で呼ぶ:\n\n" +
			"  \"${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon\" ingest-statusline <<<\"$input\" 2>/dev/null || :\n\n" +
			"stdout には何も出さず､失敗しても exit 0 で終わる (理由は stderr)｡",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestStatusline(cmd.InOrStdin(), time.Now()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-statusline:", err)
			}
			return nil
		},
	}
}

// ingestStatusline は stdin を読み切って sessionstate へ渡す｡
func ingestStatusline(stdin io.Reader, now time.Time) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	return sessionstate.New(p.StateDir()).IngestStatusline(input, now)
}
