package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIngestStop は ingest-stop を stdin 付きで実行し､stdout / stderr の中身と
// Execute の戻り値を返す｡XDG_STATE_HOME は一時ディレクトリへ向ける｡
func runIngestStop(t *testing.T, stdin string) (stdout, stderr string, stateDir string, err error) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("HOME", root)

	cmd := newIngestStopCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	err = cmd.Execute()
	return out.String(), errBuf.String(), filepath.Join(root, "agents-daemon"), err
}

const stopTranscript = `{"type":"user","timestamp":"2026-09-27T03:44:00.000Z"}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-27T03:44:05.954Z","message":{"id":"msg_1","model":"claude-fable-5-1","usage":{"cache_creation":{"ephemeral_1h_input_tokens":500,"ephemeral_5m_input_tokens":0}}}}
`

func TestNewIngestStopCmd(t *testing.T) {
	t.Run("stdin の JSON から cache/<sid>.json を書き､stdout には何も出さない", func(t *testing.T) {
		transcript := filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(transcript, []byte(stopTranscript), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		input := `{"session_id": "sess-1", "transcript_path": "` + transcript + `", "stop_hook_active": false}`

		stdout, stderr, stateDir, err := runIngestStop(t, input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Errorf("stdout = %q, stderr = %q, want both empty (正常系)", stdout, stderr)
		}
		data, readErr := os.ReadFile(filepath.Join(stateDir, "cache", "sess-1.json"))
		if readErr != nil {
			t.Fatalf("cache file が書かれていない: %v", readErr)
		}
		if !strings.Contains(string(data), `"last_request_at":"2026-09-27T03:44:05.954Z","ttl_seconds":3600,`) {
			t.Errorf("cache file = %q", data)
		}
	})

	t.Run("壊れた JSON でも exit 0 (nil) で stdout は空｡理由は stderr に出す", func(t *testing.T) {
		stdout, stderr, stateDir, err := runIngestStop(t, `{"session_id": `)
		if err != nil {
			t.Fatalf("Execute() error = %v, want nil (Stop hook は fail-open)", err)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if stderr == "" {
			t.Error("stderr が空｡手で実行したときに理由が分からない")
		}
		if _, statErr := os.Stat(filepath.Join(stateDir, "cache")); !os.IsNotExist(statErr) {
			t.Errorf("壊れた入力で cache/ が作られている: %v", statErr)
		}
	})

	t.Run("transcript が無ければ何も書かず exit 0", func(t *testing.T) {
		input := `{"session_id": "sess-1", "transcript_path": "` + filepath.Join(t.TempDir(), "missing.jsonl") + `"}`
		stdout, stderr, stateDir, err := runIngestStop(t, input)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Errorf("stdout = %q, stderr = %q, want both empty (正常な no-op)", stdout, stderr)
		}
		if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
			t.Errorf("transcript 無しで state dir が作られている: %v", statErr)
		}
	})
}
