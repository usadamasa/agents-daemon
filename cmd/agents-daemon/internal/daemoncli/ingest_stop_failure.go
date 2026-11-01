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

// newIngestStopFailureCmd は `agents-daemon ingest-stop-failure` を組み立てる。
//
// StopFailure hook (hooks/ingest-stop-failure.sh) が、turn が API error で終わるたびに stdin をそのまま渡してくる。
// 失敗しても nil を返して exit 0 にするのは、記録の失敗でセッションに割り込まないため。理由は stderr に 1 行だけ出す。
// stdout には何も出さないので --json には対応しない。
func newIngestStopFailureCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "ingest-stop-failure",
		Short:       "StopFailure hook の stdin から turn-failures/ に turn を終わらせた API error を記録する",
		Annotations: jsonflag.Unsupported(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestStopFailure(cmd.InOrStdin(), time.Now()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-stop-failure:", err)
			}
			return nil
		},
	}
}

// ingestStopFailure は stdin を読み切って記録する。
func ingestStopFailure(stdin io.Reader, now time.Time) error {
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
