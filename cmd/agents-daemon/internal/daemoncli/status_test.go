package daemoncli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/board"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

func runStatusBoard(t *testing.T, args ...string) board.Board {
	t.Helper()
	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(append([]string{"--board"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --board %v: %v", args, err)
	}
	var b board.Board
	if err := json.Unmarshal(out.Bytes(), &b); err != nil {
		t.Fatalf("JSON = %s: %v", out.String(), err)
	}
	return b
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// --json は root の persistent flag なので、root に載せて動かす。daemon が起動していなければ running=false の JSON。
func TestStatus_json(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	cmd := testRoot()
	jsonflag.Add(cmd)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var rep verify.StatusReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("JSON = %s: %v", out.String(), err)
	}
	if rep.Running {
		t.Errorf("running = true, want false: %s", out.String())
	}
}

// --board はタスクの記録を描き、--cwd の repo の行の鍵を here に置く。
func TestStatusBoard(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	home := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "state", "agents-daemon", "tasks", "W", "1.json"),
		`{"id": "1", "subject": "書く", "status": "pending", "session_id": "W", "cwd": "`+home+`"}`)

	b := runStatusBoard(t, "--cwd", home)
	if len(b.Rows) != 1 || b.Rows[0].Cards[0].Subject != "書く" || b.Here != b.Rows[0].Repo {
		t.Errorf("board = %+v", b)
	}
}
