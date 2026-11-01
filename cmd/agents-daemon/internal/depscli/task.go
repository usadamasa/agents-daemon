package depscli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// newTaskCmd は `agents-daemon task dismiss` を組み立てる。依存図の pane が、終了したセッションのタスクを一覧から
// 外すのに使う (#337)。入力は stdin の JSON で受け、成功は {} を出す。
func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "依存図に出るタスクの記録を操作する"}
	dismiss := &cobra.Command{
		Use:   "dismiss",
		Short: "stdin の TaskDismissRequest で終了したセッションのタスクの記録を消す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("TaskDismissRequest", func(p apppath.Paths, req protocol.TaskDismissRequest) (any, error) {
				exited := func(sessionID string, after time.Time) error {
					return daemonstate.SessionExited(p, sessionID, after)
				}
				return struct{}{}, dismissTask(p.StateDir(), tasklog.New(p.StateDir()), req.SessionID, req.Ref, exited, time.Now())
			}))
		},
	}
	cmd.AddCommand(dismiss)
	return cmd
}

// dismissTask は参照のタスクの記録を消し、結果を出来事のログに書く。viewer は操作したセッション。
func dismissTask(stateDir string, tasks tasklog.Store, viewer, r string, exited func(string, time.Time) error, now time.Time) error {
	sessionID, taskID, ok := ref.ParseTask(r)
	err := fmt.Errorf("参照 %q は task:<session_id>/<task_id> の形でない", r)
	if ok {
		err = tasks.Dismiss(sessionID, taskID, exited)
	}
	if err != nil {
		_ = eventlog.Record(stateDir, viewer, "task.dismiss.failed", []string{kv("ref", r), kv("reason", err)}, now)
		return err
	}
	_ = eventlog.Record(stateDir, viewer, "task.dismiss", []string{kv("from_session", sessionID), kv("task", taskID)}, now)
	return nil
}
