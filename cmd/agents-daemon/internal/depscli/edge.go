package depscli

import (
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// endpoint は stdin で受けた端点を辺の記録の端点にする。repo は edge の Add が cwd から埋める。
func endpoint(t protocol.EdgeTarget) edge.Endpoint {
	return edge.Endpoint{Ref: t.Ref, CWD: t.CWD}
}

// newEdgeCmd は `agents-daemon edge add|rm|list` を組み立てる (edge)。add と rm は stdin の JSON で受け、
// 人が引く list は flag で受ける。結果は JSON で stdout に出す。
func newEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "edge",
		Short:       "セッションをまたぐ作業の依存 (before が済まないと after を始められない) を書く・消す・一覧する",
		Annotations: jsonflag.Supported(),
	}
	sub := func(use, short string, fn func(apppath.Paths, io.Reader) (any, error)) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, fn)
		}}
	}
	add := sub("add", "stdin の EdgeAddRequest で辺を書き、EdgeAdded を出す。同じ辺の二重登録は書き直すだけ", statecmd.Decoded("EdgeAddRequest", func(p apppath.Paths, req protocol.EdgeAddRequest) (any, error) {
		return edge.New(p.StateDir()).Add(endpoint(req.Before), endpoint(req.After), req.Note, time.Now())
	}))
	rm := sub("rm", "stdin の EdgeRemoveRequest で辺を消し、EdgeRemoved を出す。無い辺は removed が false", statecmd.Decoded("EdgeRemoveRequest", func(p apppath.Paths, req protocol.EdgeRemoveRequest) (any, error) {
		return edge.New(p.StateDir()).Remove(endpoint(req.Before), endpoint(req.After))
	}))

	var cwd, repo, ref string
	var here bool
	list := sub("list", "辺を JSON の配列で出す", func(p apppath.Paths, _ io.Reader) (any, error) {
		if here {
			dir, err := statecmd.Cwd(cwd)
			if err != nil {
				return nil, err
			}
			repo = gitrepo.RepoKey(dir)
		}
		return edge.New(p.StateDir()).Find(repo, ref)
	})
	list.Flags().StringVar(&cwd, "cwd", "", "--here で使う cwd (既定は今の cwd)")
	list.Flags().StringVar(&repo, "repo", "", "どちらかの端点の repo (main worktree のパス) で絞る")
	list.Flags().BoolVar(&here, "here", false, "--cwd の repo で絞る")
	list.MarkFlagsMutuallyExclusive("repo", "here")
	list.Flags().StringVar(&ref, "ref", "", "どちらかの端点の参照で絞る")

	cmd.AddCommand(add, rm, list)
	return cmd
}
