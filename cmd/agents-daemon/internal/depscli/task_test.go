package depscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// runTask は `task <sub>` に stdin を渡す。
func runTask(t *testing.T, sub, stdin string) (stdout string, err error) {
	t.Helper()
	cmd := newTaskCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{sub})
	err = cmd.Execute()
	return out.String(), err
}

// task dismiss は task: の参照だけを受け、終了を確かめられなければ (ここでは daemon が動いていない) error にする。
// 終了の確かめの分岐は tasklog の Dismiss のテストが持つ。
func TestTaskDismissCmd(t *testing.T) {
	testEnv(t)
	for stdin, want := range map[string]string{
		`{"ref": "sid:dead"}`:            "task:<session_id>/<task_id>",
		`{"ref": "task:dead/4"}`:         "が無い",
		`{"refs": ["task:dead/4"]}`:      "refs",
		`{"ref": "task:dead/4", "x": 1}`: "x",
	} {
		if _, err := runTask(t, "dismiss", stdin); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q を含む", stdin, err, want)
		}
	}
	root := testEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	writeFile(t, filepath.Join(state, "tasks", "dead", "4.json"), `{"id": "4", "status": "pending", "session_id": "dead", "updated_at": "2026-10-18T09:00:00Z"}`)
	if _, err := runTask(t, "dismiss", `{"ref": "task:dead/4"}`); err == nil || !strings.Contains(err.Error(), "daemon が動いていない") {
		t.Errorf("error = %v, want daemon が動いていない", err)
	}
}

// 消したタスクと消さなかった理由を出来事のログに書き、subject は書かない。
func TestDismissTask_eventLog(t *testing.T) {
	dir := t.TempDir()
	tasks := tasklog.New(dir)
	writeFile(t, filepath.Join(dir, "tasks", "dead", "4.json"), `{"id": "4", "subject": "秘密のタスク", "status": "completed", "session_id": "dead"}`)
	now := time.Now()
	exited := func(string, time.Time) error { return nil }
	if err := dismissTask(dir, tasks, "viewer", "task:dead/4", exited, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "dead", "4.json")); !os.IsNotExist(err) {
		t.Errorf("記録が残った: %v", err)
	}
	if err := dismissTask(dir, tasks, "viewer", "task:dead/4", exited, now); err == nil {
		t.Fatal("消した記録をもう一度消せた")
	}
	got := eventLines(t, dir)
	equalLines(t, got, []string{
		"task.dismiss session=viewer from_session=dead task=4",
		`task.dismiss.failed session=viewer ref=task:dead/4 reason="タスク dead/4 が無い"`,
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
}
