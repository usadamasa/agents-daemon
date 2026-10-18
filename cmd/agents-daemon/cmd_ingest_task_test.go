package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngestTask は ingest-task を stdin 付きで実行する。HOME と XDG_* は一時ディレクトリへ向ける。
func runIngestTask(t *testing.T, root, stdin string) (stdout, stderr string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	cmd := newIngestTaskCmd()
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

func taskCreateInput(cwd string) string {
	return `{"session_id": "s1", "cwd": "` + cwd + `", "hook_event_name": "PostToolUse", "tool_name": "TaskCreate",` +
		` "tool_input": {"subject": "alpha", "description": "alpha"}, "tool_response": {"task": {"id": "1", "subject": "alpha"}}}`
}

// config の homePaths の下は home、それ以外は work に置く。stdout には何も出さない。
func TestIngestTaskCmd_domain(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, filepath.Join(root, "config", "agents-daemon", "config.json"), `{"homePaths": ["~/src/github.com/usadamasa"]}`)

	stdout, stderr := runIngestTask(t, root, taskCreateInput(filepath.Join(root, "src/github.com/usadamasa/agents-daemon")))
	if stdout != "" || stderr != "" {
		t.Errorf("stdout / stderr = %q / %q, want 空", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "tasks", "home", "s1", "1.json")); err != nil {
		t.Errorf("home の側に書かれていない: %v", err)
	}

	runIngestTask(t, root, strings.Replace(taskCreateInput(filepath.Join(root, "src/github.example.com/corp/app")), `"s1"`, `"s2"`, 1))
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "tasks", "work", "s2", "1.json")); err != nil {
		t.Errorf("work の側に書かれていない: %v", err)
	}
}

// 壊れた入力でも exit 0 で、理由は stderr に出す (記録の失敗でセッションに割り込まない)。
func TestIngestTaskCmd_brokenInput(t *testing.T) {
	stdout, stderr := runIngestTask(t, t.TempDir(), `{"session_id": `)
	if stdout != "" || !strings.Contains(stderr, "ingest-task:") {
		t.Errorf("stdout / stderr = %q / %q", stdout, stderr)
	}
}

func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
