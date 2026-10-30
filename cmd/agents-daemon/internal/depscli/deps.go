// Package depscli は依存図のコマンド (pane が読む deps、セッションをまたぐ辺の edge、タスクの記録の ingest-task) と、
// mod の出来事をログに書く event-log の組み立てを持つ。
package depscli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/deps"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newDepsCmd(), newEdgeCmd(), newIngestTaskCmd(), newEventLogCmd()}
}

// newDepsCmd は `agents-daemon deps` を組み立てる。依存図の JSON (deps パッケージ) を出し、plugin の mod
// (hooks/deps.tsx) が数秒ごとに読む。元から JSON なので --json を付けても変わらない。
func newDepsCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "deps",
		Short:       "stdin の DepsRequest の cwd の repo を here にした依存図 (タスクの依存の DAG、repo ごとのセッションとメモ) を DepsData で出す",
		Args:        cobra.NoArgs,
		Annotations: jsonflag.Supported(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			var req protocol.DepsRequest
			if err := protocol.Decode(cmd.InOrStdin(), "DepsRequest", &req); err != nil {
				return err
			}
			cwd, err := statecmd.Cwd(req.CWD)
			if err != nil {
				return err
			}
			return deps.Write(cmd.OutOrStdout(), p, cwd)
		},
	}
}
