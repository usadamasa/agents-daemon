package daemoncli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngestQuotaResume は ingest-quota-resume を stdin 付きで実行する。HOME と XDG_* は一時ディレクトリへ向ける。
func runIngestQuotaResume(t *testing.T, root, stdin string) (stdout, stderr string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	cmd := newIngestQuotaResumeCmd()
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

// quota_auto_resume_stale の通知を quota-resume/<session_id>.json に書く。stdout には何も出さない。
func TestIngestQuotaResumeCmd_writes(t *testing.T) {
	root := t.TempDir()
	stdout, stderr := runIngestQuotaResume(t, root,
		`{"session_id": "s1", "hook_event_name": "Notification", "message": "m", "notification_type": "quota_auto_resume_stale"}`)
	if stdout != "" || stderr != "" {
		t.Errorf("stdout / stderr = %q / %q, want 空", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "quota-resume", "s1.json")); err != nil {
		t.Errorf("記録が書かれていない: %v", err)
	}
}

// 壊れた入力でも exit 0 で、理由は stderr に出す (記録の失敗でセッションに割り込まない)。
func TestIngestQuotaResumeCmd_brokenInput(t *testing.T) {
	stdout, stderr := runIngestQuotaResume(t, t.TempDir(), `{"session_id": `)
	if stdout != "" || !strings.Contains(stderr, "ingest-quota-resume:") {
		t.Errorf("stdout / stderr = %q / %q", stdout, stderr)
	}
}
