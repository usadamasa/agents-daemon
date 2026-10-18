package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// newIngestTaskCmd は `agents-daemon ingest-task` を組み立てる。
//
// PostToolUse hook (hooks/ingest-task.sh) が Task 系ツールと TodoWrite のたびに stdin をそのまま渡してくる。
// 記録の形と置き場は internal/tasklog が持つ。失敗しても nil を返して exit 0 にするのは、記録の失敗で
// セッションの作業を止めないため。理由は stderr に 1 行だけ出す。
func newIngestTaskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ingest-task",
		Short: "PostToolUse hook の stdin からタスクの記録 (tasks/) を書く",
		Long: "Claude Code の PostToolUse hook の入力 (TaskCreate / TaskUpdate / TaskList / TodoWrite) を stdin で受け取り、\n" +
			"tasks/<domain>/<session_id>/<task_id>.json (TodoWrite は todos.json) を書く。\n" +
			"domain は config の homePaths と defaultDomain で cwd から決める。\n" +
			"stdout には何も出さず、失敗しても exit 0 で終わる (理由は stderr)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ingestTask(cmd); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest-task:", err)
			}
			return nil
		},
	}
}

func ingestTask(cmd *cobra.Command) error {
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return tasklog.Run(cmd.InOrStdin(), p, home, time.Now())
}
