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

// logs --board は logs/board.log を読み、--board なしは daemon.log を読む。
func TestLogsBoard(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	logs := filepath.Join(root, "state", "agents-daemon", "logs")
	writeFile(t, filepath.Join(logs, "board.log"), "2026-10-19T09:00:00+09:00 mod.board.failed session=H reason=x\n")
	writeFile(t, filepath.Join(logs, "daemon.log"), "2026-10-19T09:00:00+09:00 daemon の出来事\n")

	if out := runLogs(t, "--board"); !strings.Contains(out, "session=H") || strings.Contains(out, "daemon の出来事") {
		t.Errorf("--board = %q", out)
	}
	if out := runLogs(t); !strings.Contains(out, "daemon の出来事") {
		t.Errorf("--board なしは daemon.log = %q", out)
	}
}
