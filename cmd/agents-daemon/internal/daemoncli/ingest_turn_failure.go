package daemoncli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
)

// newIngestTurnFailureCmd は `agents-daemon ingest-turn-failure` を組み立てる。
//
// hooks/ingest-turn-failure.sh が StopFailure・Stop・UserPromptSubmit のたびに stdin をそのまま渡してくる。
// StopFailure で書き、残りの 2 つで消す (turnfailure.Store.Ingest)。
// 失敗しても nil を返して exit 0 にするのは、記録の失敗でセッションに割り込まないため。理由は stderr に 1 行だけ出す。
// stdout には何も出さない (UserPromptSubmit の stdout は会話の context に入る) ので --json には対応しない。
func newIngestTurnFailureCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "ingest-turn-failure",
		Short:       "hook の stdin から、turn を終わらせた API error を turn-failures/ に記録し、次の turn で消す",
		Annotations: jsonflag.Unsupported(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestTurnFailure(cmd.InOrStdin(), time.Now()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-turn-failure:", err)
			}
			return nil
		},
	}
}

// ingestTurnFailure は stdin を読み切って記録する。
func ingestTurnFailure(stdin io.Reader, now time.Time) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	return turnfailure.New(p.StateDir()).Ingest(input, now)
}
