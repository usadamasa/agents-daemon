package depscli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/deps"
)

func runDeps(t *testing.T, args ...string) deps.Deps {
	t.Helper()
	cmd := newDepsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("deps %v: %v", args, err)
	}
	var d deps.Deps
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("JSON = %s: %v", out.String(), err)
	}
	return d
}

// deps はタスクの記録から依存図を組み、--cwd の repo の鍵を here に置く。
func TestDeps(t *testing.T) {
	root := testEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "state", "agents-daemon", "tasks", "W", "1.json"),
		`{"id": "1", "subject": "書く", "status": "pending", "session_id": "W", "cwd": "`+home+`"}`)

	d := runDeps(t, "--cwd", home)
	if len(d.Repos) != 1 || d.Here != d.Repos[0].Repo || len(d.Graph.Nodes) != 1 || d.Graph.Nodes[0].Title != "書く" {
		t.Errorf("deps = %+v", d)
	}
}
