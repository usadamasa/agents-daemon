package daemoncli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/quotaresume"
)

// newIngestQuotaResumeCmd は `agents-daemon ingest-quota-resume` を組み立てる。
//
// hooks/ingest-quota-resume.sh が Notification (quota_auto_resume_*) のたびに stdin をそのまま渡してくる。
// 失敗しても nil を返して exit 0 にするのは、記録の失敗でセッションに割り込まないため。理由は stderr に 1 行だけ出す。
// hook の stdout は Claude Code が読むので何も出さず、--json には対応しない。
func newIngestQuotaResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "ingest-quota-resume",
		Short:       "Notification hook の stdin から、本体の上限待ちの結末 (quota_auto_resume_*) を quota-resume/ に記録する",
		Annotations: jsonflag.Unsupported(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestQuotaResume(cmd.InOrStdin(), time.Now()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-quota-resume:", err)
			}
			return nil
		},
	}
}

// ingestQuotaResume は stdin を読み切って記録する。
func ingestQuotaResume(stdin io.Reader, now time.Time) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	return quotaresume.New(p.StateDir()).Ingest(input, now)
}
