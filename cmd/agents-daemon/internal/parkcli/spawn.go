package parkcli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/spawn"
)

// newSpawnCmd は `agents-daemon spawn` を組み立てる (spawn)。判定は spawn.Runner が持ち、ここは stdin と設定を
// 渡すだけ。
func newSpawnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "spawn",
		Short: "stdin の SpawnRequest の prompt を最初のターンにした Claude Code のセッションを、repo を cwd にした新しい rex の session に起こす",
		Long: "パーキングロットのメモを、生きているセッションの無い repo へ渡すときに呼ぶ。\n" +
			"新しいセッションは `claude --session-id <uuid> --model <model> -- <本文>` で起きる。<model> は\n" +
			"Claude Code の settings.json の env." + spawn.SubagentModelKey + " から読む。\n" +
			"claude が前景になってから SpawnResult を JSON で出す。待ち切れたときは session を残したまま\n" +
			"foreground=false と detail を出す (error にするのは session を作れなかったときだけ)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("SpawnRequest", func(paths apppath.Paths, req protocol.SpawnRequest) (any, error) {
				return spawnSession(cmd.Context(), paths, req.Repo, req.Prompt)
			}))
		},
	}
}

// spawnSession は repo で新しいセッションを起こし、prompt を最初のターンにする。依存図のアプリは rex の外で起きるので
// REX_SESSION を要求しない (ConnectRex)。rex の session は spawn が起こすたびに新しく作る。
func spawnSession(ctx context.Context, paths apppath.Paths, repo, prompt string) (spawn.Result, error) {
	host, ok := backend.ConnectRex(sessioncontext.New(paths.StateDir())).(spawn.Host)
	if !ok {
		return spawn.Result{}, errors.New("この backend は session を作れない")
	}
	configDir, err := apppath.ClaudeConfigDir()
	if err != nil {
		return spawn.Result{}, err
	}
	model, err := spawn.SubagentModel(configDir)
	if err != nil {
		return spawn.Result{}, err
	}
	return spawn.Runner{Host: host}.Run(ctx, spawn.Request{Repo: repo, Prompt: prompt, Model: model})
}
