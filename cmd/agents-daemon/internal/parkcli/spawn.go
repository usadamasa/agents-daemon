package parkcli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/spawn"
)

// subagentModelEnv は新しいセッションのモデルを取る環境変数。ハーネスの settings の env が定義し、Claude Code の
// 子プロセス (mod の process.run) に見える。別セッションへ渡す作業は subagent 用のモデルに揃える (peer-sessions skill)。
const subagentModelEnv = "CLAUDE_CODE_SUBAGENT_MODEL"

// newSpawnCmd は `agents-daemon spawn` を組み立てる (spawn)。判定は spawn.Runner が持ち、ここは stdin と環境を
// 渡すだけ。
func newSpawnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "spawn",
		Short: "stdin の SpawnRequest の prompt を最初のターンにした Claude Code のセッションを、repo を cwd にした rex の window に起こす",
		Long: "パーキングロットのメモを、生きているセッションの無い repo へ渡すときに mod が呼ぶ。\n" +
			"新しいセッションは `claude --session-id <uuid> --model $" + subagentModelEnv + " -- <本文>` で起き、\n" +
			"claude が前景になってから SpawnResult を JSON で出す。待ち切れたときは window を残したまま\n" +
			"foreground=false と detail を出す (error にするのは window を作れなかったときだけ)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("SpawnRequest", func(paths apppath.Paths, req protocol.SpawnRequest) (any, error) {
				_, client, err := backend.Connect(os.Getenv, sessioncontext.New(paths.StateDir()))
				if err != nil {
					return nil, err
				}
				host, ok := client.(spawn.Host)
				if !ok {
					return nil, errors.New("この backend は window を作れない")
				}
				return spawn.Runner{Host: host}.Run(cmd.Context(), spawn.Request{
					Repo:               req.Repo,
					Prompt:             req.Prompt,
					Model:              os.Getenv(subagentModelEnv),
					MultiplexerSession: os.Getenv("REX_SESSION"),
				})
			}))
		},
	}
}
