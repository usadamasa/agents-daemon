package boardcli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/backend"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/spawn"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// subagentModelEnv は新しいセッションのモデルを取る環境変数。ハーネスの settings の env が定義し、Claude Code の
// 子プロセス (mod の process.run) に見える。別セッションへ渡す作業は subagent 用のモデルに揃える (peer-sessions skill)。
const subagentModelEnv = "CLAUDE_CODE_SUBAGENT_MODEL"

// newSpawnCmd は `agents-daemon spawn` を組み立てる (internal/spawn)。判定は spawn.Runner が持ち、ここは flag と環境を
// 渡すだけ。domain の決め方は park と同じ (withPlacer)。
func newSpawnCmd() *cobra.Command {
	var repo, session, cwd string
	cmd := &cobra.Command{
		Use:   "spawn",
		Short: "stdin の本文を最初のターンにした Claude Code のセッションを、--repo を cwd にした rex の window に起こす",
		Long: "ボードのパーキングのメモを、生きているセッションの無い repo へ渡すときに mod が呼ぶ。\n" +
			"新しいセッションは `claude --session-id <uuid> --model $" + subagentModelEnv + " -- <本文>` で起き、\n" +
			"claude が前景になってから {session_id, pane_id, foreground} を JSON で出す。待ち切れたときは window を残したまま\n" +
			"foreground=false と detail を出す (error にするのは window を作れなかったときだけ)。\n" +
			"--repo の domain が開いたセッション (--session と --cwd) の domain と違えば起こさない (私用と業務の分離)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("stdin の本文を読めない: %w", err)
			}
			return withPlacer(cmd, cwd, func(paths apppath.Paths, p tasklog.Placer, cwd string) (any, error) {
				_, client, err := backend.Connect(os.Getenv, sessioncontext.New(paths.StateDir()))
				if err != nil {
					return nil, err
				}
				host, ok := client.(spawn.Host)
				if !ok {
					return nil, errors.New("この backend は window を作れない")
				}
				runner := spawn.Runner{Host: host, Domain: p.Domain}
				return runner.Run(cmd.Context(), spawn.Request{
					Repo:               repo,
					SessionID:          session,
					CWD:                cwd,
					Prompt:             string(prompt),
					Model:              os.Getenv(subagentModelEnv),
					MultiplexerSession: os.Getenv("REX_SESSION"),
				})
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "新しいセッションの cwd (絶対パス)")
	cmd.Flags().StringVar(&session, "session", "", "開いたセッションの ID (domain を決める)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "開いたセッションの cwd (既定は今の cwd)")
	_ = cmd.MarkFlagRequired("repo")
	return cmd
}
