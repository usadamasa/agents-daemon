package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngestTask は ingest-task を stdin 付きで実行する。HOME と XDG の各ディレクトリは一時ディレクトリへ向ける。
func runIngestTask(t *testing.T, stdin, config string) (stdout, stderr, stateDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	if config != "" {
		path := filepath.Join(root, "config", "agents-daemon", "config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := newIngestTaskCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, want nil (記録の失敗でセッションを止めない)", err)
	}
	return out.String(), errBuf.String(), filepath.Join(root, "state", "agents-daemon")
}

const taskCreateInput = `{"session_id": "sess-1", "cwd": "/src/home/repo", "hook_event_name": "PostToolUse", "tool_name": "TaskCreate",
 "tool_input": {"subject": "alpha", "description": "alpha"}, "tool_response": {"task": {"id": "1", "subject": "alpha"}}}`

func TestNewIngestTaskCmd(t *testing.T) {
	t.Run("config の homePaths で domain を決めて書き、stdout には何も出さない", func(t *testing.T) {
		stdout, stderr, stateDir := runIngestTask(t, taskCreateInput, `{"homePaths": ["/src/home"]}`)
		if stdout != "" || stderr != "" {
			t.Errorf("stdout = %q, stderr = %q, want 両方空", stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(stateDir, "tasks", "home", "sess-1", "1.json")); err != nil {
			t.Errorf("私用の側に書かれていない: %v", err)
		}
	})

	t.Run("config が無ければ業務に倒す", func(t *testing.T) {
		_, _, stateDir := runIngestTask(t, taskCreateInput, "")
		if _, err := os.Stat(filepath.Join(stateDir, "tasks", "work", "sess-1", "1.json")); err != nil {
			t.Errorf("業務の側に書かれていない: %v", err)
		}
	})

	t.Run("壊れた JSON でも exit 0 で、理由は stderr に出す", func(t *testing.T) {
		stdout, stderr, stateDir := runIngestTask(t, `{"session_id": `, "")
		if stdout != "" || stderr == "" {
			t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(stateDir, "tasks")); !os.IsNotExist(err) {
			t.Errorf("壊れた入力で tasks/ が作られた: %v", err)
		}
	})
}
