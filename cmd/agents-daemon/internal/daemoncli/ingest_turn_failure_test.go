package daemoncli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngestTurnFailure は ingest-turn-failure を stdin 付きで実行する。HOME と XDG_* は一時ディレクトリへ向ける。
func runIngestTurnFailure(t *testing.T, root, stdin string) (stdout, stderr string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	cmd := newIngestTurnFailureCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v (失敗しても exit 0 のはず)", err)
	}
	return out.String(), errBuf.String()
}

// StopFailure は turn-failures/<session_id>.json に書き、Stop はそれを消す。stdout には何も出さない。
func TestIngestTurnFailureCmd_writesAndClears(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state", "agents-daemon", "turn-failures", "s1.json")

	stdout, stderr := runIngestTurnFailure(t, root, `{"session_id": "s1", "hook_event_name": "StopFailure", "error": "rate_limit"}`)
	if stdout != "" || stderr != "" {
		t.Errorf("stdout / stderr = %q / %q, want 空", stdout, stderr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("記録が書かれていない: %v", err)
	}

	stdout, stderr = runIngestTurnFailure(t, root, `{"session_id": "s1", "hook_event_name": "Stop"}`)
	if stdout != "" || stderr != "" {
		t.Errorf("stdout / stderr = %q / %q, want 空", stdout, stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stop の後も記録が残っている: %v", err)
	}
}

// 壊れた入力でも exit 0 で、理由は stderr に出す (記録の失敗でセッションに割り込まない)。
func TestIngestTurnFailureCmd_brokenInput(t *testing.T) {
	stdout, stderr := runIngestTurnFailure(t, t.TempDir(), `{"session_id": `)
	if stdout != "" || !strings.Contains(stderr, "ingest-turn-failure:") {
		t.Errorf("stdout / stderr = %q / %q", stdout, stderr)
	}
}
