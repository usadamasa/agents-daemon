package tasklog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestPrune(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, "task-create-alpha", "task-create-beta", "task-update-in-progress", "task-update-completed")
	dir := filepath.Join(s.Dir, fixtureSession)
	now := time.Now()

	// alpha (完了) は 1 日を過ぎ、beta (未完了) は 1 日を過ぎたが 1 週間は経っていない。
	setMtime(t, filepath.Join(dir, "1.json"), now.Add(-25*time.Hour))
	setMtime(t, filepath.Join(dir, "2.json"), now.Add(-25*time.Hour))
	removed, err := s.Prune(now, nil)
	if err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件 (完了したタスクだけ)", removed, err)
	}
	if got := byID(t, s); len(got) != 1 || got["2"].ID != "2" {
		t.Errorf("残ったタスク = %+v, want 2 だけ", got)
	}

	// 1 週間更新されなかった未完了のタスクも消え、空になったセッションのディレクトリも消える。
	setMtime(t, filepath.Join(dir, "2.json"), now.Add(-8*24*time.Hour))
	if removed, err := s.Prune(now, nil); err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件", removed, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("空のセッションのディレクトリが残っている: %v", err)
	}
}

// keepCompleted が true のセッションの完了タスクは、Retention を過ぎても消さない。未完了のタスクは消す。
func TestPrune_keepCompleted(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, "task-create-alpha", "task-create-beta", "task-update-in-progress", "task-update-completed")
	dir := filepath.Join(s.Dir, fixtureSession)
	now := time.Now()
	setMtime(t, filepath.Join(dir, "1.json"), now.Add(-8*24*time.Hour))
	setMtime(t, filepath.Join(dir, "2.json"), now.Add(-8*24*time.Hour))

	var asked []string
	keep := func(sessionID string) bool {
		asked = append(asked, sessionID)
		return true
	}
	if removed, err := s.Prune(now, keep); err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件 (未完了のタスクだけ)", removed, err)
	}
	if got := byID(t, s); len(got) != 1 || got["1"].Status != "completed" {
		t.Errorf("残ったタスク = %+v, want 完了した 1 だけ", got)
	}
	if len(asked) == 0 || asked[0] != fixtureSession {
		t.Errorf("keepCompleted に渡したセッション = %v, want %s", asked, fixtureSession)
	}

	// 残す理由が無くなれば、次の掃除で消える。
	if removed, err := s.Prune(now, func(string) bool { return false }); err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1 件", removed, err)
	}
}

func TestPrune_nothingYet(t *testing.T) {
	if removed, err := New(t.TempDir()).Prune(time.Now(), nil); err != nil || removed != 0 {
		t.Errorf("Prune() = %d, %v, want 0, nil", removed, err)
	}
}
