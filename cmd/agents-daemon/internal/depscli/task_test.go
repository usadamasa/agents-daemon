package depscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/worklog"
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

// task dismiss は task: と sid: の参照を受け、終了を確かめられなければ (ここでは daemon が動いていない) error にする。
// 終了の確かめの分岐は tasklog と worklog の Dismiss のテストが持つ。
func TestTaskDismissCmd(t *testing.T) {
	testEnv(t)
	for stdin, want := range map[string]string{
		`{"ref": "todo:dead/0"}`:         "task:<session_id>/<task_id> か sid:<session_id>",
		`{"ref": "sid:dead"}`:            "作業の記録が無い",
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
	writeFile(t, filepath.Join(state, "work", "gone.json"), `{"session_id": "gone", "cwd": "/w", "subject": "s", "turns": 1, "started_at": "2026-10-18T09:00:00Z", "updated_at": "2026-10-18T09:00:00Z"}`)
	for _, stdin := range []string{`{"ref": "task:dead/4"}`, `{"ref": "sid:gone"}`} {
		if _, err := runTask(t, "dismiss", stdin); err == nil || !strings.Contains(err.Error(), "daemon が動いていない") {
			t.Errorf("%s: error = %v, want daemon が動いていない", stdin, err)
		}
	}
}

// 消したタスク・作業と消さなかった理由を出来事のログに書き、subject は書かない。
func TestDismiss_eventLog(t *testing.T) {
	dir := t.TempDir()
	tasks := tasklog.New(dir)
	works := worklog.New(dir)
	writeFile(t, filepath.Join(dir, "tasks", "dead", "4.json"), `{"id": "4", "subject": "秘密のタスク", "status": "completed", "session_id": "dead"}`)
	writeFile(t, filepath.Join(dir, "work", "gone.json"), `{"session_id": "gone", "cwd": "/w", "subject": "秘密の作業", "turns": 1, "started_at": "2026-10-18T09:00:00Z", "updated_at": "2026-10-18T09:00:00Z"}`)
	now := time.Now()
	exited := func(string, time.Time) error { return nil }
	for _, r := range []string{"task:dead/4", "sid:gone"} {
		if err := dismiss(dir, tasks, works, "viewer", r, exited, now); err != nil {
			t.Fatal(err)
		}
		if err := dismiss(dir, tasks, works, "viewer", r, exited, now); err == nil {
			t.Fatalf("%s: 消した記録をもう一度消せた", r)
		}
	}
	for _, path := range []string{filepath.Join(dir, "tasks", "dead", "4.json"), filepath.Join(dir, "work", "gone.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("記録が残った: %s %v", path, err)
		}
	}
	got := eventLines(t, dir)
	equalLines(t, got, []string{
		"task.dismiss session=viewer from_session=dead task=4",
		`task.dismiss.failed session=viewer ref=task:dead/4 reason="タスク dead/4 が無い"`,
		"work.dismiss session=viewer from_session=gone",
		`work.dismiss.failed session=viewer ref=sid:gone reason="セッション gone の作業の記録が無い"`,
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
}
