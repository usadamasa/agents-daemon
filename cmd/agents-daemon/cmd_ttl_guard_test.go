package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runTTLGuard は ttl-guard を stdin 付きで実行し､stdout / stderr の中身と Execute の戻り値を返す｡
func runTTLGuard(t *testing.T, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("HOME", root)

	cmd := newTTLGuardCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

func writeGuardTranscript(t *testing.T, minutesAgo int) string {
	t.Helper()
	ts := time.Now().Add(-time.Duration(minutesAgo) * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	line := `{"type":"assistant","isSidechain":false,"timestamp":"` + ts +
		`","message":{"id":"msg_1","model":"claude-fable-5-1","usage":{"cache_creation":{"ephemeral_1h_input_tokens":500,"ephemeral_5m_input_tokens":0}}}}` + "\n"
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func guardInput(transcript, prompt string) string {
	return `{"session_id": "sess-1", "transcript_path": "` + transcript + `", "prompt": "` + prompt + `"}`
}

func TestNewTTLGuardCmd(t *testing.T) {
	t.Run("失効していれば警告を stderr に出し exit 2 を返す｡stdout は空", func(t *testing.T) {
		stdout, stderr, err := runTTLGuard(t, guardInput(writeGuardTranscript(t, 61), "hello"))
		var ec exitCodeError
		if !errors.As(err, &ec) || ec.code != 2 {
			t.Fatalf("Execute() error = %v, want exitCodeError{2}", err)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty (exit 0 以外でも context に入れない)", stdout)
		}
		if !strings.Contains(stderr, "/clear") {
			t.Errorf("stderr = %q, want 警告文", stderr)
		}
	})

	t.Run("warm なら exit 0 で何も出さない", func(t *testing.T) {
		stdout, stderr, err := runTTLGuard(t, guardInput(writeGuardTranscript(t, 30), "hello"))
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Errorf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
		}
	})

	t.Run("壊れた入力は exit 0 (fail-open) で理由を stderr に出す", func(t *testing.T) {
		stdout, stderr, err := runTTLGuard(t, `{"prompt": `)
		if err != nil {
			t.Fatalf("Execute() error = %v, want nil (止めない)", err)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if stderr == "" {
			t.Error("stderr が空｡手で実行したときに理由が分からない")
		}
	})
}
