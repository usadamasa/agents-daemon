package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// newIngestStopCmd は `agents-daemon ingest-stop` を組み立てる｡
//
// Stop hook (hooks/cache-state.sh) が応答の終わりごとに stdin をそのまま渡してくる｡
// 失敗しても nil を返して exit 0 にするのは､hook が fail-open でなければならず
// (非 0 は Claude Code がエラーとして扱う)､stdout も判定 JSON として読まれるため｡
// 理由は stderr に 1 行だけ出し､手で実行したときに追えるようにする｡
func newIngestStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ingest-stop",
		Short: "Stop hook の stdin から cache/ の state file を書く",
		Long: "Claude Code が Stop hook へ渡す JSON を stdin で受け取り､transcript_path の末尾から\n" +
			"直近の応答の開始時刻と最新の cache write の TTL を読んで､daemon が読む\n" +
			"cache/<session_id>.json を書く｡cache write が無ければ消す｡\n" +
			"stdout には何も出さず､失敗しても exit 0 で終わる (理由は stderr)｡",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestStop(cmd.InOrStdin()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-stop:", err)
			}
			return nil
		},
	}
}

// ingestStop は stdin を読み切って sessionstate へ渡す｡
func ingestStop(stdin io.Reader) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	return sessionstate.New(p.StateDir()).IngestStop(input)
}
