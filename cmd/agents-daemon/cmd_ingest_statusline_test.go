package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngest は ingest-statusline を stdin 付きで実行し､stdout / stderr の中身と
// Execute の戻り値を返す｡XDG_STATE_HOME は一時ディレクトリへ向ける｡
func runIngest(t *testing.T, stdin string) (stdout, stderr string, stateDir string, err error) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("HOME", root)

	cmd := newIngestStatuslineCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	err = cmd.Execute()
	return out.String(), errBuf.String(), filepath.Join(root, "agents-daemon"), err
}

func TestNewIngestStatuslineCmd(t *testing.T) {
	t.Run("stdin の JSON から context/<sid>.json を書き､stdout には何も出さない", func(t *testing.T) {
		stdout, _, stateDir, err := runIngest(t, `{"session_id": "sess-1", "context_window": {"used_percentage": 42.6, "context_window_size": 200000}}`)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty (statusline の描画を汚す)", stdout)
		}
		data, readErr := os.ReadFile(filepath.Join(stateDir, "context", "sess-1.json"))
		if readErr != nil {
			t.Fatalf("context file が書かれていない: %v", readErr)
		}
		if !strings.Contains(string(data), `"used_percentage":43,`) {
			t.Errorf("context file = %q, want used_percentage 43", data)
		}
		if !strings.Contains(string(data), `"context_window_size":200000`) {
			t.Errorf("context file = %q, want context_window_size 200000", data)
		}
	})

	t.Run("壊れた JSON でも exit 0 (nil) で stdout は空｡理由は stderr に出す", func(t *testing.T) {
		stdout, stderr, stateDir, err := runIngest(t, `{"session_id": `)
		if err != nil {
			t.Fatalf("Execute() error = %v, want nil (statusline は毎描画で呼ぶので fail-silent)", err)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if stderr == "" {
			t.Error("stderr が空｡手で実行したときに理由が分からない")
		}
		if _, statErr := os.Stat(filepath.Join(stateDir, "context")); !os.IsNotExist(statErr) {
			t.Errorf("壊れた入力で context/ が作られている: %v", statErr)
		}
	})

	t.Run("session_id が無ければ何も書かず exit 0", func(t *testing.T) {
		stdout, stderr, stateDir, err := runIngest(t, `{"context_window": {"used_percentage": 42}}`)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Errorf("stdout = %q, stderr = %q, want both empty (正常系)", stdout, stderr)
		}
		if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
			t.Errorf("session_id 無しで state dir が作られている: %v", statErr)
		}
	})
}
