package daemoncli

import (
	"bytes"
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

// logs --events は logs/events.log を読み、--events なしは daemon.log を読む。
func TestLogsEvents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	logs := filepath.Join(root, "state", "agents-daemon", "logs")
	writeFile(t, filepath.Join(logs, "events.log"), "2026-10-19T09:00:00+09:00 mod.deps.failed session=H reason=x\n")
	writeFile(t, filepath.Join(logs, "daemon.log"), "2026-10-19T09:00:00+09:00 daemon の出来事\n")

	if out := runLogs(t, "--events"); !strings.Contains(out, "session=H") || strings.Contains(out, "daemon の出来事") {
		t.Errorf("--events = %q", out)
	}
	if out := runLogs(t); !strings.Contains(out, "daemon の出来事") {
		t.Errorf("--events なしは daemon.log = %q", out)
	}
}
