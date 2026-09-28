package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// exitCodeError は main に終了コードだけを伝える｡メッセージは RunE が出し終えている｡
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// newTTLGuardCmd は `agents-daemon ttl-guard` を組み立てる｡
//
// UserPromptSubmit hook (hooks/ttl-guard.sh) が stdin をそのまま渡してくる｡止めるときだけ
// 警告を stderr に出して exit 2 にする (Claude Code が prompt を消して stderr を見せる)｡
// 失敗は止めずに exit 0 で返し､理由を stderr に 1 行出す｡stdout は exit 0 で context に
// 入るので何も出さない｡
func newTTLGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ttl-guard",
		Short: "UserPromptSubmit hook の stdin を見て､prompt cache の失効後の最初の prompt を止める",
		Long: "Claude Code が UserPromptSubmit hook へ渡す JSON を stdin で受け取り､transcript から\n" +
			"prompt cache が失効しているかを判定する｡失効後の最初の prompt なら警告を stderr に出して\n" +
			"exit 2 で終わる｡それ以外と失敗は exit 0 (失敗の理由は stderr)｡",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			warning, err := ttlGuard(cmd.InOrStdin())
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ttl-guard:", err)
			}
			if warning == "" {
				return nil
			}
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), warning)
			return exitCodeError{code: 2}
		},
	}
}

func ttlGuard(stdin io.Reader) (string, error) {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return "", err
	}
	return sessionstate.New(p.StateDir()).TTLGuard(input, time.Now())
}
