package parking

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

func homePlacer(stateDir string) tasklog.Placer {
	return tasklog.Placer{Tasks: tasklog.New(stateDir), Classify: func(string) domain.Domain { return domain.Home }}
}

func writeTask(t *testing.T, stateDir string, d domain.Domain, task tasklog.Task) string {
	t.Helper()
	dir := filepath.Join(stateDir, "tasks", string(d), task.SessionID)
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

// 終了したセッションの未完了のタスクは、kind task のメモにしてからタスクの記録を消す。
func TestAdopt_movesTaskToParking(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	path := writeTask(t, stateDir, domain.Home, tasklog.Task{ID: "4", Subject: "README を直す", Description: "表の列がずれている", Status: "in_progress", SessionID: "dead", CWD: cwd})
	s := New(stateDir)

	added, err := s.Adopt("viewer", cwd, "dead", "4", homePlacer(stateDir), testNow)
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	item := added.Item
	if !added.Created || item.Kind != KindTask || item.Title != "README を直す" || item.Detail != "表の列がずれている" || item.Repo != cwd || item.Domain != domain.Home {
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

// 完了したタスク、記録の無いタスク、別の domain のタスクは移さない。
func TestAdopt_rejects(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	done := writeTask(t, stateDir, domain.Home, tasklog.Task{ID: "1", Subject: "済み", Status: "completed", SessionID: "dead", CWD: cwd})
	work := writeTask(t, stateDir, domain.Work, tasklog.Task{ID: "2", Subject: "業務", Status: "pending", SessionID: "busy", CWD: cwd})
	s := New(stateDir)
	p := homePlacer(stateDir)

	for _, c := range []struct{ name, session, task string }{
		{"完了", "dead", "1"},
		{"記録が無い", "dead", "9"},
		{"別の domain", "busy", "2"},
		{"ファイル名に使えない", "../x", "1"},
	} {
		if _, err := s.Adopt("viewer", cwd, c.session, c.task, p, testNow); err == nil {
			t.Errorf("%s: Adopt() error = nil", c.name)
		}
	}
	for _, path := range []string{done, work} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("移さなかったタスクの記録が消えた: %v", err)
		}
	}
	if got, _ := s.list(domain.Home); len(got) != 0 {
		t.Errorf("メモができた: %+v", got)
	}
}

// kind task は park adopt だけが置く。model の tool (park add) からは置けない。
func TestPark_rejectsTaskKind(t *testing.T) {
	stateDir := t.TempDir()
	req := Request{Entry: Entry{Kind: KindTask, Title: "t"}, SessionID: "s1"}
	if _, err := New(stateDir).Park(req, t.TempDir(), homePlacer(stateDir), testNow); err == nil {
		t.Errorf("Park() が kind task を通した")
	}
}
