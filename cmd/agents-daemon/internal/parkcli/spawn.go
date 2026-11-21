package parkcli

import (
	"context"
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

// newSpawnCmd は `agents-daemon spawn` を組み立てる (spawn)。判定は spawn.Runner が持ち、ここは stdin と設定を
// 渡すだけ。
func newSpawnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "spawn",
		Short: "stdin の SpawnRequest の本文を最初のターンにした Claude Code のセッションを、repo を cwd にして rex に起こす",
		Long: "peer-sessions skill が peer を起こすとき、依存図のアプリがメモを生きているセッションの無い repo へ渡すときに呼ぶ。\n" +
			"新しいセッションは `claude --session-id <uuid> --model <model> -- <本文>` で起きる。<model> は model の欄、\n" +
			"無ければ Claude Code の settings.json の env." + spawn.SubagentModelKey + " から読む。\n" +
			"fork_from があれば `--resume <fork_from> --fork-session` を足し、元の会話を写した新しいセッションとして起こす。\n" +
			"起こす先は session の欄があればその rex の session のタブ、new_session なら新しい rex の session、\n" +
			"どちらも無ければ REX_SESSION (起こす側の session) のタブ、REX_SESSION も無ければ新しい rex の session。\n" +
			"claude が前景になってから SpawnResult を JSON で出す。待ち切れたときは session を残したまま\n" +
			"foreground=false と detail を出す (error にするのは session を作れなかったときだけ)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("SpawnRequest", func(paths apppath.Paths, req protocol.SpawnRequest) (any, error) {
				return spawnSession(cmd.Context(), paths, spawnCommandRequest(req))
			}))
		},
	}
}

// spawnCommandRequest は spawn サブコマンドの stdin を spawn.Request にする。起こす側の rex session (REX_SESSION) を
// 足し先の既定にするのはこのサブコマンドだけ。依存図のアプリは launchd から起き、REX_SESSION を持たない。
func spawnCommandRequest(req protocol.SpawnRequest) spawn.Request {
	return spawn.Request{
		Repo: req.Repo, Prompt: req.Prompt, PromptFile: req.PromptFile, Session: req.Session, NewSession: req.NewSession,
		SessionID: req.SessionID, ForkFrom: req.ForkFrom, Model: req.Model, Label: req.Label, Caller: os.Getenv("REX_SESSION"),
	}
}

// handSpawnRequest は park hand が起こすセッションの spawn.Request。足し先を持たず、rex の中のセッションから
// 呼ばれても新しい rex session に起こす。
func handSpawnRequest(repo, prompt string) spawn.Request {
	return spawn.Request{Repo: repo, Prompt: prompt}
}

// spawnSession は req のセッションを起こす。依存図のアプリは rex の外で起きるので REX_SESSION を要求しない
// (ConnectRex)。req.Model が空なら settings の subagent 用のモデルを使う。
func spawnSession(ctx context.Context, paths apppath.Paths, req spawn.Request) (spawn.Result, error) {
	client := backend.ConnectRex(sessioncontext.New(paths.StateDir()))
	defer backend.Close(client)
	host, ok := client.(spawn.Host)
	if !ok {
		return spawn.Result{}, errors.New("この backend は session を作れない")
	}
	defaultModel := func() (string, error) {
		configDir, err := apppath.ClaudeConfigDir()
		if err != nil {
			return "", err
		}
		return spawn.SubagentModel(configDir)
	}
	return spawn.Runner{Host: host, DefaultModel: defaultModel}.Run(ctx, req)
}
