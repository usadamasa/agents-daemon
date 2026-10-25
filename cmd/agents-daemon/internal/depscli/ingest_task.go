package depscli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// newIngestTaskCmd は `agents-daemon ingest-task` を組み立てる。
//
// PostToolUse hook (hooks/ingest-task.sh) が Task 系ツールと TodoWrite の呼び出しのたびに stdin を
// そのまま渡してくる。失敗しても nil を返して exit 0 にするのは、記録の失敗でセッションの作業に
// 割り込まないため。理由は stderr に 1 行だけ出す。stdout には何も出さない (Claude Code が読む)。
func newIngestTaskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ingest-task",
		Short: "PostToolUse hook の stdin から tasks/ にタスクを記録する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestTask(cmd.InOrStdin(), time.Now()); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-task:", err)
			}
			return nil
		},
	}
}

// ingestTask は stdin を読み切って記録する。
func ingestTask(stdin io.Reader, now time.Time) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	res, err := tasklog.New(p.StateDir()).Ingest(input, now)
	logIngest(p.StateDir(), res, err, now)
	return err
}

// logIngest は ingest-task の結果を出来事のログ (internal/eventlog) に書く。記録しないツールは書かない。
// eventlog に持たせないのは、eventlog が tasklog を import しないため (tasklog の Ca を増やさない)。
func logIngest(stateDir string, res tasklog.Result, err error, now time.Time) {
	if err == nil && res.Tool == "" {
		return
	}
	var fields []string
	if res.AgentID != "" {
		fields = append(fields, kv("agent", res.AgentID))
	}
	if res.Tool != "" {
		fields = append(fields, kv("tool", res.Tool))
	}
	if err != nil {
		_ = eventlog.Record(stateDir, res.SessionID, "ingest-task.failed", append(fields, kv("reason", err)), now)
		return
	}
	_ = eventlog.Record(stateDir, res.SessionID, "ingest-task", append(fields, kv("written", res.Written), kv("removed", res.Removed)), now)
}

func kv(key string, value any) string {
	return key + "=" + fmt.Sprint(value)
}
