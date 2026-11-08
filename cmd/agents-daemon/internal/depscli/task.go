package depscli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// newTaskCmd は `agents-daemon task dismiss` を組み立てる。依存図のアプリが、終了したセッションのタスク (task:) か
// タスクの無いセッションの作業 (sid:、#368) を一覧から外すのに使う (#337)。入力は stdin の JSON で受け、成功は {} を出す。
func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "依存図に出るタスクと作業の記録を操作する"}
	dismiss := &cobra.Command{
		Use:   "dismiss",
		Short: "stdin の TaskDismissRequest で終了したセッションのタスクか作業の記録を消す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("TaskDismissRequest", func(p apppath.Paths, req protocol.TaskDismissRequest) (any, error) {
				exited := func(sessionID string, after time.Time) error {
					return daemonstate.SessionExited(p, sessionID, after)
				}
				return struct{}{}, dismiss(p.StateDir(), tasklog.New(p.StateDir()), worklog.New(p.StateDir()), edge.New(p.StateDir()), req.SessionID, req.Ref, exited, time.Now())
			}))
		},
	}
	cmd.AddCommand(dismiss)
	return cmd
}

// dismiss は参照のタスク (task:) か作業 (sid:) の記録を消し、結果を出来事のログに書く。viewer は操作したセッション。
// sid: はそのセッションを端点に持つ辺も消す (#484)。
func dismiss(stateDir string, tasks tasklog.Store, works worklog.Store, edges edge.Store, viewer, r string, exited func(string, time.Time) error, now time.Time) error {
	if sessionID, ok := ref.ParseSessionID(r); ok {
		removed, err := dismissSession(works, edges, sessionID, r, exited)
		if err != nil {
			_ = eventlog.Record(stateDir, viewer, "work.dismiss.failed", []string{kv("ref", r), kv("reason", err)}, now)
			return err
		}
		_ = eventlog.Record(stateDir, viewer, "work.dismiss", []string{kv("from_session", sessionID), kv("edges", removed)}, now)
		return nil
	}
	sessionID, taskID, ok := ref.ParseTask(r)
	err := fmt.Errorf("参照 %q は task:<session_id>/<task_id> か sid:<session_id> の形でない", r)
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

// dismissSession は終了したセッションの作業の記録と、そのセッションを端点に持つ辺を消し、消した辺の数を返す。
// 作業の記録が無くても辺があれば外せる。終了の確認は何かを消す前に済ませ、確かめられなければ何も消さない。
func dismissSession(works worklog.Store, edges edge.Store, sessionID, r string, exited func(string, time.Time) error) (int, error) {
	found, err := edges.Find("", r)
	if err != nil {
		return 0, err
	}
	if len(found) > 0 {
		if err := exited(sessionID, found[len(found)-1].CreatedAt); err != nil {
			return 0, fmt.Errorf("セッション %s の辺は消さない: %w", sessionID, err)
		}
	}
	if _, ok := works.Get(sessionID); ok || len(found) == 0 {
		if err := works.Dismiss(sessionID, exited); err != nil {
			return 0, err
		}
	}
	removed, err := edges.RemoveByEndpoint(r)
	return len(removed), err
}
