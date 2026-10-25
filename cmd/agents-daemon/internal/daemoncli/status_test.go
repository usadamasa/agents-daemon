package daemoncli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

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
