package depscli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// newIngestWorkCmd は `agents-daemon ingest-work` を組み立てる。
//
// SessionStart / UserPromptSubmit / Stop / SessionEnd hook (hooks/ingest-work.sh) が stdin をそのまま渡してくる。どの event で
// 何を書くかはバイナリ側 (worklog) が持つ。失敗しても nil を返して exit 0 にするのは、記録の失敗でセッションの作業に
// 割り込まないため。理由は stderr に 1 行だけ出す。stdout には何も出さない (UserPromptSubmit と SessionStart の stdout は
// 会話の context に入る) ので --json には対応しない。
func newIngestWorkCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "ingest-work",
		Short:       "SessionStart / UserPromptSubmit / Stop / SessionEnd hook の stdin から work/ にセッションの作業を記録する",
		Annotations: jsonflag.Unsupported(),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestWork(cmd.InOrStdin(), time.Now()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-work:", err)
			}
			return nil
		},
	}
}

// ingestWork は stdin を読み切って記録する。
func ingestWork(stdin io.Reader, now time.Time) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	res, err := worklog.New(p.StateDir()).Ingest(input, now)
	logIngestWork(p.StateDir(), res, err, now)
	return err
}

// logIngestWork は ingest-work の結果を出来事のログに書く。毎ターン書くと行が伸びるだけなので、記録を作ったとき、
// SessionEnd で ended を書いたとき、失敗したときだけ書く。
func logIngestWork(stateDir string, res worklog.Result, err error, now time.Time) {
	switch {
	case err != nil:
		_ = eventlog.Record(stateDir, res.SessionID, "ingest-work.failed", []string{kv("event", res.Event), kv("reason", err)}, now)
	case res.Created:
		_ = eventlog.Record(stateDir, res.SessionID, "ingest-work", []string{kv("event", res.Event), kv("created", true)}, now)
	case res.Ended:
		_ = eventlog.Record(stateDir, res.SessionID, "ingest-work", []string{kv("event", res.Event), kv("ended", true)}, now)
	}
}
