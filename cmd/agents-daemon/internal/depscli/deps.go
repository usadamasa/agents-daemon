// Package depscli は依存図のコマンド (pane が読む deps、セッションをまたぐ辺の edge、タスクの記録の ingest-task) と、
// mod の出来事をログに書く event-log の組み立てを持つ。
package depscli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/deps"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newDepsCmd(), newEdgeCmd(), newIngestTaskCmd(), newEventLogCmd()}
}

// newDepsCmd は `agents-daemon deps` を組み立てる。依存図の JSON (internal/deps) を出し、plugin の mod
// (hooks/deps.tsx) が数秒ごとに読む。元から JSON なので --json を付けても変わらない。
func newDepsCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:         "deps",
		Short:       "依存図 (タスクの依存の DAG、repo ごとのセッションとメモ) を JSON で出す",
		Args:        cobra.NoArgs,
		Annotations: jsonflag.Supported(),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			if cwd == "" {
				if cwd, err = os.Getwd(); err != nil {
					return err
				}
			}
			return deps.Write(cmd.OutOrStdout(), p, cwd)
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "pane を開いたセッションの cwd。この repo を here にする (既定は今の cwd)")
	return cmd
}
