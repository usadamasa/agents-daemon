package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// newEdgeCmd は `agents-daemon edge add|rm|list` を組み立てる (internal/edge)。結果は JSON で stdout に出す。
// add と rm は両端の cwd から domain を決め、書く側のセッションの domain は見ない (記録に入るのは両端の情報だけ)。
func newEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "edge", Short: "セッションをまたぐ作業の依存 (before が済まないと after を始められない) を書く・消す・一覧する"}

	var before, after edge.Endpoint
	// endpointCmd は両端を flag で受け、run に置き場と cwd の domain の決め方を渡す add / rm を作る。
	endpointCmd := func(use, short string, run func(edge.Store, func(string) domain.Domain) (any, error)) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			return withPlacer(cmd, "", func(p apppath.Paths, placer tasklog.Placer, _ string) (any, error) {
				return run(edge.New(p.StateDir()), placer.Classify)
			})
		}}
		c.Flags().StringVar(&before.Ref, "before", "", "先に済ませる作業の参照 (session:<名前> / issue:<owner>/<repo>#<番号> / pr:<owner>/<repo>#<番号> / task:<session_id>/<task_id>)")
		c.Flags().StringVar(&before.CWD, "before-cwd", "", "before の作業のセッションの cwd (絶対パス)")
		c.Flags().StringVar(&after.Ref, "after", "", "before の後に始める作業の参照")
		c.Flags().StringVar(&after.CWD, "after-cwd", "", "after の作業のセッションの cwd (絶対パス)")
		for _, name := range []string{"before", "before-cwd", "after", "after-cwd"} {
			_ = c.MarkFlagRequired(name)
		}
		return c
	}
	var note string
	add := endpointCmd("add", "辺を書き、{created, edge} を出す。同じ辺の二重登録は書き直すだけ", func(s edge.Store, classify func(string) domain.Domain) (any, error) {
		return s.Add(before, after, note, classify, time.Now())
	})
	add.Flags().StringVar(&note, "note", "", "辺の補足 (何の後か)")
	rm := endpointCmd("rm", "辺を消し、{removed, id} を出す。無い辺は removed が false", func(s edge.Store, classify func(string) domain.Domain) (any, error) {
		return s.Remove(before, after, classify)
	})

	var cwd, session, repo, ref string
	var here bool
	list := &cobra.Command{
		Use:   "list",
		Short: "--cwd の domain の辺を JSON の配列で出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPlacer(cmd, cwd, func(p apppath.Paths, placer tasklog.Placer, cwd string) (any, error) {
				if here {
					repo = gitrepo.RepoKey(cwd)
				}
				return edge.New(p.StateDir()).Find(placer.Domain(session, cwd), repo, ref)
			})
		},
	}
	list.Flags().StringVar(&cwd, "cwd", "", "読むセッションの cwd。domain をここから決める (既定は今の cwd)")
	list.Flags().StringVar(&session, "session", "", "読むセッションの ID。記録が業務の側にあれば業務の辺を出す (status --board と同じ)")
	list.Flags().StringVar(&repo, "repo", "", "どちらかの端点の repo (main worktree のパス) で絞る")
	list.Flags().BoolVar(&here, "here", false, "--cwd の repo で絞る")
	list.MarkFlagsMutuallyExclusive("repo", "here")
	list.Flags().StringVar(&ref, "ref", "", "どちらかの端点の参照で絞る")

	cmd.AddCommand(add, rm, list)
	return cmd
}
