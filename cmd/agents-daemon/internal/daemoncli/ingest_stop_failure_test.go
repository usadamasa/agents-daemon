package daemoncli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngestStopFailure は ingest-stop-failure を stdin 付きで実行する。HOME と XDG_* は一時ディレクトリへ向ける。
func runIngestStopFailure(t *testing.T, root, stdin string) (stdout, stderr string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	cmd := newIngestStopFailureCmd()
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

// 記録は turn-failures/<session_id>.json に置く。stdout には何も出さない。
func TestIngestStopFailureCmd_writes(t *testing.T) {
	root := t.TempDir()
	stdout, stderr := runIngestStopFailure(t, root, `{"session_id": "s1", "hook_event_name": "StopFailure", "error": "rate_limit"}`)
	if stdout != "" || stderr != "" {
		t.Errorf("stdout / stderr = %q / %q, want 空", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "turn-failures", "s1.json")); err != nil {
		t.Errorf("記録が書かれていない: %v", err)
	}
}

// 壊れた入力でも exit 0 で、理由は stderr に出す (記録の失敗でセッションに割り込まない)。
func TestIngestStopFailureCmd_brokenInput(t *testing.T) {
	stdout, stderr := runIngestStopFailure(t, t.TempDir(), `{"session_id": `)
	if stdout != "" || !strings.Contains(stderr, "ingest-stop-failure:") {
		t.Errorf("stdout / stderr = %q / %q", stdout, stderr)
	}
}
