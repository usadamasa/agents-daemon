package depscli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

func TestLogIngest(t *testing.T) {
	dir := t.TempDir()
	logIngest(dir, tasklog.Result{Tool: "TaskList", SessionID: "s1", Written: 2, Removed: 1}, nil, time.Now())
	logIngest(dir, tasklog.Result{Tool: "TaskCreate", SessionID: "s1", AgentID: "a1", Written: 1}, nil, time.Now())
	// 記録しないツールは書かない。
	logIngest(dir, tasklog.Result{}, nil, time.Now())
	equalLines(t, eventLines(t, dir), []string{
		"ingest-task session=s1 tool=TaskList written=2 removed=1",
		"ingest-task session=s1 agent=a1 tool=TaskCreate written=1 removed=0",
	})

	logIngest(dir, tasklog.Result{}, errors.New("hook 入力の JSON を読めない"), time.Now())
	logIngest(dir, tasklog.Result{Tool: "TaskCreate", SessionID: "s2"}, errors.New(`task ID "../x" はファイル名に使えない`), time.Now())
	got := eventLines(t, dir)
	if len(got) != 4 || got[2] != `ingest-task.failed reason="hook 入力の JSON を読めない"` ||
		got[3] != `ingest-task.failed session=s2 tool=TaskCreate reason="task ID \"../x\" はファイル名に使えない"` {
		t.Errorf("lines = %q", got)
	}
}

func runEventLog(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newEventLogCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// event-log は mod の出来事を出来事のログへ書く。
func TestEventLog(t *testing.T) {
	root := testEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	if err := runEventLog(t, "--session", "s1", "--", "mod.deps.failed", "reason=exit 1"); err != nil {
		t.Fatalf("event-log: %v", err)
	}
	if err := runEventLog(t, "--session", "s1", "--", "Bad Event"); err == nil {
		t.Errorf("出来事の名前でないものを通した")
	}
	if err := runEventLog(t, "--session", "s2", "--", "mod.hint.failed", "reason=x"); err != nil {
		t.Fatal(err)
	}
	equalLines(t, eventLines(t, state), []string{`mod.deps.failed session=s1 reason="exit 1"`, "mod.hint.failed session=s2 reason=x"})
}

// ingest-task は記録した結果を出来事のログに書く。subject は書かない。
func TestIngestTaskWritesEventLog(t *testing.T) {
	root := testEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	runIngestTask(t, root, taskCreateInput(cwd))

	log := strings.Join(eventLines(t, filepath.Join(root, "state", "agents-daemon")), "\n")
	if want := "ingest-task session=s1 tool=TaskCreate written=1 removed=0"; !strings.Contains(log, want) {
		t.Errorf("events.log に %q が無い:\n%s", want, log)
	}
	if strings.Contains(log, "alpha") {
		t.Errorf("subject を書いた:\n%s", log)
	}
}
