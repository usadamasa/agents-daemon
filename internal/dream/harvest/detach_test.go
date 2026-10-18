package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitFor は cond が真になるまで最大 5 秒待つ。detach した子の完了待ちに使う。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("条件が時間内に満たされない")
}

func TestSpawnDetached_ReturnsBeforeChildEndsAndLogsItsStderr(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	logPath := DetachLogPath(logDir)

	start := time.Now()
	// 子は 2 秒眠ってから stderr に 1 行書く。親はそれを待たずに戻る。
	err := SpawnDetached("/bin/sh", []string{"-c", "sleep 2; printf 'child says hi\\n' >&2"}, logPath)
	if err != nil {
		t.Fatalf("SpawnDetached: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("親が子を待っている (%s)", elapsed)
	}

	waitFor(t, func() bool {
		data, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(data), "child says hi")
	})
}

func TestSpawnDetached_AppendsToExistingLog(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	logPath := DetachLogPath(logDir)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("earlier line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SpawnDetached("/bin/sh", []string{"-c", "printf 'later line\\n'"}, logPath); err != nil {
		t.Fatalf("SpawnDetached: %v", err)
	}
	waitFor(t, func() bool {
		data, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(data), "later line")
	})
	data, _ := os.ReadFile(logPath)
	if !strings.HasPrefix(string(data), "earlier line\n") {
		t.Errorf("既存の行が消えた: %q", data)
	}
}

func TestDetachLogPath(t *testing.T) {
	if got, want := DetachLogPath("/state/logs"), "/state/logs/dream-harvest.log"; got != want {
		t.Errorf("DetachLogPath = %q; want %q", got, want)
	}
}
