// Package depscli は依存図のコマンド (アプリと mod が読む deps、セッションをまたぐ辺の edge、タスクの記録の ingest-task と
// task dismiss) と、
// mod の出来事をログに書く event-log の組み立てを持つ。
package depscli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/deps"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newDepsCmd(), newEdgeCmd(), newTaskCmd(), newIngestTaskCmd(), newEventLogCmd()}
}

// newDepsCmd は `agents-daemon deps` を組み立てる。依存図の JSON (deps パッケージ) を出し、アプリ (gui/) が数秒ごとに、
// 渡した宛先の辺を確かめる mod (hooks/handoff-notice.ts) が SendMessage のたびに読む。元から JSON なので --json を
// 付けても変わらない。
func newDepsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deps",
		Short: "依存図 (タスクの依存の DAG、repo ごとのセッションとメモ) を DepsData で出す。stdin は DepsRequest ({})",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			var req protocol.DepsRequest
			if err := protocol.Decode(cmd.InOrStdin(), "DepsRequest", &req); err != nil {
				return err
			}
			return deps.Write(cmd.OutOrStdout(), p)
		},
	}
}
