package parking

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

func writeTask(t *testing.T, stateDir string, task tasklog.Task) string {
	t.Helper()
	dir := filepath.Join(stateDir, "tasks", task.SessionID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, task.ID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// exited は「終了したと確かめた」の代わり。
func exited(string, time.Time) error { return nil }

// 終了したセッションの未完了のタスクは、kind task のメモにしてからタスクの記録を消す。
func TestAdopt_movesTaskToParking(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	path := writeTask(t, stateDir, tasklog.Task{ID: "4", Subject: "README を直す", Description: "表の列がずれている", Status: "in_progress", SessionID: "dead", CWD: cwd})
	s := New(stateDir)

	added, err := s.Adopt("dead", "4", tasklog.New(stateDir), exited, testNow)
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	item := added.Item
	if !added.Created || item.Kind != KindTask || item.Title != "README を直す" || item.Detail != "表の列がずれている" || item.Repo != cwd {
		t.Errorf("item = %+v", item)
	}
	if item.DefaultTaken != AdoptedDefault {
		t.Errorf("default_taken = %q", item.DefaultTaken)
	}
	if len(item.Sources) != 1 || item.Sources[0].SessionID != "dead" || item.Sources[0].TaskID != "4" || item.Sources[0].CWD != cwd || !item.Sources[0].At.Equal(testNow) {
		t.Errorf("sources = %+v", item.Sources)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("タスクの記録が残った: %v", err)
	}
}

// 完了したタスクと記録の無いタスクは移さない。
func TestAdopt_rejects(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	done := writeTask(t, stateDir, tasklog.Task{ID: "1", Subject: "済み", Status: "completed", SessionID: "dead", CWD: cwd})
	s := New(stateDir)
	p := tasklog.New(stateDir)

	for _, c := range []struct{ name, session, task string }{
		{"完了", "dead", "1"},
		{"記録が無い", "dead", "9"},
		{"ファイル名に使えない", "../x", "1"},
	} {
		if _, err := s.Adopt(c.session, c.task, p, exited, testNow); err == nil {
			t.Errorf("%s: Adopt() error = nil", c.name)
		}
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("移さなかったタスクの記録が消えた: %v", err)
	}
	if got, _ := s.list(); len(got) != 0 {
		t.Errorf("メモができた: %+v", got)
	}
}

// セッションが終了したと確かめられなければ (daemon が止まっている、pane がある)、メモを書かず記録も消さない
// (models/handoff の LiveTaskKept)。確かめる側にはタスクの記録の時刻を渡す。
func TestAdopt_requiresExitedSession(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	updated := testNow.Add(-time.Hour)
	path := writeTask(t, stateDir, tasklog.Task{ID: "4", Subject: "まだ動いている", Status: "in_progress", SessionID: "alive", CWD: cwd, UpdatedAt: updated})
	s := New(stateDir)
	var askedFor string
	var askedAfter time.Time
	alive := func(sessionID string, after time.Time) error {
		askedFor, askedAfter = sessionID, after
		return errors.New("セッション alive は生きている")
	}

	_, err := s.Adopt("alive", "4", tasklog.New(stateDir), alive, testNow)
	if err == nil || !strings.Contains(err.Error(), "生きている") {
		t.Fatalf("Adopt() error = %v, want 生きている", err)
	}
	if askedFor != "alive" || !askedAfter.Equal(updated) {
		t.Errorf("確かめた対象 = %q / %v, want alive / %v", askedFor, askedAfter, updated)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("タスクの記録が消えた: %v", err)
	}
	if got, _ := s.list(); len(got) != 0 {
		t.Errorf("メモができた: %+v", got)
	}
}

// kind task は park adopt だけが置く。model の tool (park add) からは置けない。
func TestPark_rejectsTaskKind(t *testing.T) {
	stateDir := t.TempDir()
	req := Request{Entry: Entry{Kind: KindTask, Title: "t"}, SessionID: "s1"}
	if _, err := New(stateDir).Park(req, t.TempDir(), tasklog.New(stateDir), testNow); err == nil {
		t.Errorf("Park() が kind task を通した")
	}
}
