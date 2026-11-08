package depscli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
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
		if err := dismiss(dir, tasks, works, edge.New(dir), "viewer", r, exited, now); err != nil {
			t.Fatal(err)
		}
		if err := dismiss(dir, tasks, works, edge.New(dir), "viewer", r, exited, now); err == nil {
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
		"work.dismiss session=viewer from_session=gone edges=0",
		`work.dismiss.failed session=viewer ref=sid:gone reason="セッション gone の作業の記録が無い"`,
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
}

// sid: を外すと、before でも after でもそのセッションを端点に持つ辺が消える。ほかの辺は残る。
// 作業の記録が無く辺の端点だけで残っているセッションも外せる (#484)。
func TestDismiss_sessionEdges(t *testing.T) {
	for name, withWork := range map[string]bool{"辺の端点だけ": false, "作業の記録あり": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			edges := edge.New(dir)
			addEdge(t, edges, "sid:gone", "pr:o/r#1")
			addEdge(t, edges, "sid:peer", "sid:gone")
			addEdge(t, edges, "sid:peer", "pr:o/r#1")
			work := filepath.Join(dir, "work", "gone.json")
			if withWork {
				writeFile(t, work, `{"session_id": "gone", "cwd": "/w", "subject": "s", "turns": 1, "started_at": "2026-10-18T09:00:00Z", "updated_at": "2026-10-18T09:00:00Z"}`)
			}
			exited := func(string, time.Time) error { return nil }
			if err := dismiss(dir, tasklog.New(dir), worklog.New(dir), edges, "viewer", "sid:gone", exited, time.Now()); err != nil {
				t.Fatal(err)
			}
			got, err := edges.Find("", "")
			if err != nil || len(got) != 1 || got[0].Before.Ref != "sid:peer" || got[0].After.Ref != "pr:o/r#1" {
				t.Errorf("残った辺 = %+v, %v", got, err)
			}
			if _, err := os.Stat(work); !os.IsNotExist(err) {
				t.Errorf("作業の記録が残った: %v", err)
			}
			equalLines(t, eventLines(t, dir), []string{"work.dismiss session=viewer from_session=gone edges=2"})
		})
	}
}

// 終了を確かめられないセッションの辺と作業の記録は消さない。確かめる時刻には端点の辺の最も新しい created_at を渡す
// (辺を書いた後に status.json が更新されていなければ、終了したとは言えない)。
func TestDismiss_sessionEdgesKeptUnlessExited(t *testing.T) {
	dir := t.TempDir()
	edges := edge.New(dir)
	addEdge(t, edges, "sid:alive", "pr:o/r#1")
	writeFile(t, filepath.Join(dir, "work", "alive.json"), `{"session_id": "alive", "cwd": "/w", "subject": "s", "turns": 1, "started_at": "2026-10-18T09:00:00Z", "updated_at": "2026-10-18T09:00:00Z"}`)
	var checked []time.Time
	exited := func(_ string, after time.Time) error {
		checked = append(checked, after)
		return errors.New("生きている")
	}
	err := dismiss(dir, tasklog.New(dir), worklog.New(dir), edges, "viewer", "sid:alive", exited, time.Now())
	if err == nil || !strings.Contains(err.Error(), "生きている") {
		t.Fatalf("error = %v", err)
	}
	if got, _ := edges.Find("", ""); len(got) != 1 {
		t.Errorf("辺が消えた: %+v", got)
	}
	if _, ok := worklog.New(dir).Get("alive"); !ok {
		t.Errorf("作業の記録が消えた")
	}
	if len(checked) == 0 || !checked[0].Equal(testEdgeTime) {
		t.Errorf("確かめた時刻 = %v, want %v", checked, testEdgeTime)
	}
}

// testEdgeTime は addEdge が辺に書く created_at。
var testEdgeTime = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func addEdge(t *testing.T, s edge.Store, before, after string) {
	t.Helper()
	if _, err := s.Add(edge.Endpoint{Ref: before, CWD: "/w"}, edge.Endpoint{Ref: after, CWD: "/w"}, "", testEdgeTime); err != nil {
		t.Fatal(err)
	}
}
