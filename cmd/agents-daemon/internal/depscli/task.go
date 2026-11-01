package depscli

import (
	"fmt"
	"strings"
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
// 外すのに使う (#337)。入力は stdin の JSON で受け、結果は JSON で stdout に出す。
func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "依存図に出るタスクの記録を操作する"}
	dismiss := &cobra.Command{
		Use:   "dismiss",
		Short: "stdin の TaskDismissRequest で終了したセッションのタスクの記録を消し、TaskDismissed を出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("TaskDismissRequest", func(p apppath.Paths, req protocol.TaskDismissRequest) (any, error) {
				exited := func(sessionID string, after time.Time) error {
					return daemonstate.SessionExited(p, sessionID, after)
				}
				return dismissTasks(p.StateDir(), tasklog.New(p.StateDir()), req, exited, time.Now()), nil
			}))
		},
	}
	cmd.AddCommand(dismiss)
	return cmd
}

// dismissTasks は refs のタスクを 1 つずつ消す。消せない参照があっても残りは続け、理由を failures に返す
// (まとめて消すとき、1 つのセッションが生き返っていてもほかを止めない)。
func dismissTasks(stateDir string, tasks tasklog.Store, req protocol.TaskDismissRequest, exited func(string, time.Time) error, now time.Time) protocol.TaskDismissed {
	res := protocol.TaskDismissed{Dismissed: []string{}, Failures: []protocol.TaskDismissFailure{}}
	for _, r := range req.Refs {
		err := dismissTask(tasks, r, exited)
		if err != nil {
			res.Failures = append(res.Failures, protocol.TaskDismissFailure{Ref: r, Reason: err.Error()})
			_ = eventlog.Record(stateDir, req.SessionID, "task.dismiss.failed", []string{kv("ref", r), kv("reason", err)}, now)
			continue
		}
		res.Dismissed = append(res.Dismissed, r)
	}
	if len(res.Dismissed) > 0 {
		_ = eventlog.Record(stateDir, req.SessionID, "task.dismiss", []string{kv("tasks", len(res.Dismissed)), kv("refs", strings.Join(res.Dismissed, ","))}, now)
	}
	return res
}

func dismissTask(tasks tasklog.Store, r string, exited func(string, time.Time) error) error {
	sessionID, taskID, ok := ref.ParseTask(r)
	if !ok {
		return fmt.Errorf("参照 %q は task:<session_id>/<task_id> の形でない", r)
	}
	return tasks.Dismiss(sessionID, taskID, exited)
}
