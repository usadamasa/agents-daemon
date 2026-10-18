package daemoncli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runLogs(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newLogsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs %v: %v", args, err)
	}
	return out.String()
}

// logs --board はボードを開いたセッションの domain (status --board と同じ規則) のログだけを読む。業務の側からは
// 私用の出来事が見えず、私用の側からは業務の出来事が見えない。
func TestLogsBoard_domain(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(root, "config", "agents-daemon", "config.json"), `{"homePaths": ["~/src/home"]}`)
	logs := filepath.Join(root, "state", "agents-daemon", "logs")
	writeFile(t, filepath.Join(logs, "board-home.log"), "2026-10-19T09:00:00+09:00 mod.board.failed session=H reason=x\n")
	writeFile(t, filepath.Join(logs, "board-work.log"), "2026-10-19T09:00:00+09:00 mod.hint.failed session=W reason=y\n")
	writeFile(t, filepath.Join(logs, "daemon.log"), "2026-10-19T09:00:00+09:00 daemon の出来事\n")
	writeFile(t, filepath.Join(root, "state", "agents-daemon", "tasks", "work", "W", "1.json"),
		`{"id": "1", "subject": "業務", "status": "pending", "session_id": "W"}`)
	home := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}

	if out := runLogs(t, "--board", "--cwd", home); !strings.Contains(out, "session=H") || strings.Contains(out, "session=W") {
		t.Errorf("私用の cwd = %q", out)
	}
	if out := runLogs(t, "--board", "--cwd", filepath.Join(root, "work")); !strings.Contains(out, "session=W") || strings.Contains(out, "session=H") {
		t.Errorf("業務の cwd = %q", out)
	}
	if out := runLogs(t, "--board", "--cwd", home, "--session", "W"); !strings.Contains(out, "session=W") {
		t.Errorf("業務に倒したセッション = %q", out)
	}
	if out := runLogs(t); !strings.Contains(out, "daemon の出来事") {
		t.Errorf("--board なしは daemon.log = %q", out)
	}
}
