package depscli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
)

// sessionIDEnv は Claude Code が Bash ツールの環境に入れる、そのセッション自身の ID。task の端点の参照を組む唯一の
// 情報源にする (scripts/get-session-id.sh と同じ)。推測はせず、無ければエラーにする。
// cc-premise: 2.1.314 bash-env Bash ツールの環境に CLAUDE_CODE_SESSION_ID が入る
const sessionIDEnv = "CLAUDE_CODE_SESSION_ID"

// endpoints は stdin で受けた両端を辺の記録の端点にする。repo は edge の Add が cwd から埋める。
func endpoints(before, after protocol.EdgeTarget) (edge.Endpoint, edge.Endpoint, error) {
	b, err := endpoint(before)
	if err != nil {
		return edge.Endpoint{}, edge.Endpoint{}, fmt.Errorf("before: %w", err)
	}
	a, err := endpoint(after)
	if err != nil {
		return edge.Endpoint{}, edge.Endpoint{}, fmt.Errorf("after: %w", err)
	}
	return b, a, nil
}

// endpoint は端点を ref か task のどちらか 1 つで受ける。ref の端点はそのまま使い、cwd も要る。task の端点は書く側の
// セッション自身のタスクで、参照を task:<session_id>/<id> にここで組み (mod と model は組まない)、cwd を省けば
// バイナリの cwd にする。参照の形は edge の Add / Remove が確かめる。
func endpoint(t protocol.EdgeTarget) (edge.Endpoint, error) {
	switch {
	case t.Ref != "" && t.Task != "":
		return edge.Endpoint{}, fmt.Errorf("端点は ref と task のどちらか 1 つで指す (ref %q と task %q の両方がある)", t.Ref, t.Task)
	case t.Ref != "":
		if t.CWD == "" {
			return edge.Endpoint{}, fmt.Errorf("ref %q の端点には cwd (その作業のセッションの cwd) が要る", t.Ref)
		}
		return edge.Endpoint{Ref: t.Ref, CWD: t.CWD}, nil
	case t.Task != "":
		sessionID := strings.TrimSpace(os.Getenv(sessionIDEnv))
		if sessionID == "" {
			return edge.Endpoint{}, fmt.Errorf("task %q の参照を組む %s が環境に無い。Claude Code のセッションの Bash から書くか、ref に task:<session_id>/<task_id> を書く", t.Task, sessionIDEnv)
		}
		cwd, err := statecmd.Cwd(t.CWD)
		if err != nil {
			return edge.Endpoint{}, err
		}
		return edge.Endpoint{Ref: ref.Task(sessionID, t.Task), CWD: cwd}, nil
	default:
		return edge.Endpoint{}, errors.New("端点は ref (cwd も要る) か task のどちらかで指す")
	}
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
	add := sub("add", "stdin の EdgeAddRequest で辺を書き、EdgeAdded を出す。同じ辺の二重登録は書き直すだけ。端点の task 欄は自分のセッションのタスク", statecmd.Decoded("EdgeAddRequest", func(p apppath.Paths, req protocol.EdgeAddRequest) (any, error) {
		before, after, err := endpoints(req.Before, req.After)
		if err != nil {
			return nil, err
		}
		return edge.New(p.StateDir()).Add(before, after, req.Note, time.Now())
	}))
	rm := sub("rm", "stdin の EdgeRemoveRequest で辺を消し、EdgeRemoved を出す。無い辺は removed が false", statecmd.Decoded("EdgeRemoveRequest", func(p apppath.Paths, req protocol.EdgeRemoveRequest) (any, error) {
		before, after, err := endpoints(req.Before, req.After)
		if err != nil {
			return nil, err
		}
		return edge.New(p.StateDir()).Remove(before, after)
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
