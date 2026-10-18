package tasklog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
)

// age は path の mtime を now から d だけ前にする。
func age(t *testing.T, path string, now time.Time, d time.Duration) {
	t.Helper()
	if err := os.Chtimes(path, now.Add(-d), now.Add(-d)); err != nil {
		t.Fatal(err)
	}
}

func TestPrune(t *testing.T) {
	now := time.Now()
	s := New(t.TempDir())
	// alpha (1) は completed、beta (2) は pending。
	ingest(t, s, allHome, "task-create-alpha", "task-create-beta", "task-update-completed")
	dir := filepath.Join(s.Dir, "home", fixtureSession)
	alpha, beta := filepath.Join(dir, "1.json"), filepath.Join(dir, "2.json")

	age(t, alpha, now, CompletedRetention-time.Minute)
	age(t, beta, now, CompletedRetention+time.Minute)
	if n, err := s.Prune(now); err != nil || n != 0 {
		t.Fatalf("保持期間内で Prune() = %d, %v", n, err)
	}

	// 完了したタスクは CompletedRetention、未完了は Retention で消える。
	age(t, alpha, now, CompletedRetention)
	if n, err := s.Prune(now); err != nil || n != 1 {
		t.Fatalf("完了したタスクの Prune() = %d, %v", n, err)
	}
	if _, err := os.Stat(alpha); !os.IsNotExist(err) {
		t.Errorf("完了したタスクが残っている")
	}
	if _, err := os.Stat(beta); err != nil {
		t.Errorf("未完了のタスクが 1 日で消えた: %v", err)
	}

	age(t, beta, now, Retention)
	if n, err := s.Prune(now); err != nil || n != 1 {
		t.Fatalf("未完了のタスクの Prune() = %d, %v", n, err)
	}
	// 空になったセッションのディレクトリも消す。domain のディレクトリは残してよい。
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("空のセッションのディレクトリが残っている: %v", err)
	}
	if tasks, err := s.List(domain.Home); err != nil || len(tasks) != 0 {
		t.Errorf("List() = %+v, %v", tasks, err)
	}
}

func TestPrune_todosAndTempFiles(t *testing.T) {
	now := time.Now()
	s := New(t.TempDir())
	dir := filepath.Join(s.Dir, "work", "sid")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	todos, tmp := filepath.Join(dir, todosFile), filepath.Join(dir, ".task.123")
	for _, p := range []string{todos, tmp} {
		// completed と書いてあっても、todos.json と一時ファイルはタスクとして読まない。
		if err := os.WriteFile(p, []byte(`{"id":"x","status":"completed"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, p, now, CompletedRetention+time.Hour)
	}
	if n, err := s.Prune(now); err != nil || n != 0 {
		t.Fatalf("Retention 前の Prune() = %d, %v", n, err)
	}
	for _, p := range []string{todos, tmp} {
		age(t, p, now, Retention)
	}
	if n, err := s.Prune(now); err != nil || n != 2 {
		t.Fatalf("Retention 後の Prune() = %d, %v", n, err)
	}
}

func TestPrune_noDir(t *testing.T) {
	if n, err := New(filepath.Join(t.TempDir(), "missing")).Prune(time.Now()); err != nil || n != 0 {
		t.Errorf("tasks/ が無いときの Prune() = %d, %v", n, err)
	}
	if n, err := (Store{}).Prune(time.Now()); err != nil || n != 0 {
		t.Errorf("ゼロ値の Prune() = %d, %v", n, err)
	}
}
