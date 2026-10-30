package depscli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
)

// newEdgeCmd は `agents-daemon edge add|rm|list` を組み立てる (edge)。結果は JSON で stdout に出す。
func newEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "edge",
		Short:       "セッションをまたぐ作業の依存 (before が済まないと after を始められない) を書く・消す・一覧する",
		Annotations: jsonflag.Supported(),
	}

	var before, after edge.Endpoint
	// endpointCmd は両端を flag で受け、run に置き場を渡す add / rm を作る。
	endpointCmd := func(use, short string, run func(edge.Store) (any, error)) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			return statecmd.Run(cmd, "", func(p apppath.Paths, _ string) (any, error) {
				return run(edge.New(p.StateDir()))
			})
		}}
		c.Flags().StringVar(&before.Ref, "before", "", "先に済ませる作業の参照 (session:<名前> / sid:<session_id> / issue:<owner>/<repo>#<番号> / pr:<owner>/<repo>#<番号> / task:<session_id>/<task_id>)")
		c.Flags().StringVar(&before.CWD, "before-cwd", "", "before の作業のセッションの cwd (絶対パス)")
		c.Flags().StringVar(&after.Ref, "after", "", "before の後に始める作業の参照")
		c.Flags().StringVar(&after.CWD, "after-cwd", "", "after の作業のセッションの cwd (絶対パス)")
		for _, name := range []string{"before", "before-cwd", "after", "after-cwd"} {
			_ = c.MarkFlagRequired(name)
		}
		return c
	}
	var note string
	add := endpointCmd("add", "辺を書き、{created, edge} を出す。同じ辺の二重登録は書き直すだけ", func(s edge.Store) (any, error) {
		return s.Add(before, after, note, time.Now())
	})
	add.Flags().StringVar(&note, "note", "", "辺の補足 (何の後か)")
	rm := endpointCmd("rm", "辺を消し、{removed, id} を出す。無い辺は removed が false", func(s edge.Store) (any, error) {
		return s.Remove(before, after)
	})

	var cwd, repo, ref string
	var here bool
	list := &cobra.Command{
		Use:   "list",
		Short: "辺を JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return statecmd.Run(cmd, cwd, func(p apppath.Paths, cwd string) (any, error) {
				if here {
					repo = gitrepo.RepoKey(cwd)
				}
				return edge.New(p.StateDir()).Find(repo, ref)
			})
		},
	}
	list.Flags().StringVar(&cwd, "cwd", "", "--here で使う cwd (既定は今の cwd)")
	list.Flags().StringVar(&repo, "repo", "", "どちらかの端点の repo (main worktree のパス) で絞る")
	list.Flags().BoolVar(&here, "here", false, "--cwd の repo で絞る")
	list.MarkFlagsMutuallyExclusive("repo", "here")
	list.Flags().StringVar(&ref, "ref", "", "どちらかの端点の参照で絞る")

	cmd.AddCommand(add, rm, list)
	return cmd
}
