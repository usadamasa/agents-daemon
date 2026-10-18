package daemoncli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/board"
	"github.com/usadamasa/agents-daemon/internal/domain"
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

// --board の domain は config の homePaths と --cwd で決め、--session の記録が業務の側にあれば業務を描く。
// 設定が無ければ homePaths の下でも業務を描く。
func TestStatusBoard_domain(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	config := filepath.Join(root, "config", "agents-daemon", "config.json")
	writeFile(t, config, `{"homePaths": ["~/src/home"]}`)
	home := filepath.Join(root, "src", "home", "a")
	work := filepath.Join(root, "work", "b")
	for _, d := range []string{home, work} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, "state", "agents-daemon", "tasks", "work", "W", "1.json"),
		`{"id": "1", "subject": "業務", "status": "pending", "session_id": "W"}`)

	if b := runStatusBoard(t, "--cwd", home); b.Domain != domain.Home {
		t.Errorf("私用の cwd: domain = %q", b.Domain)
	}
	if b := runStatusBoard(t, "--cwd", work); b.Domain != domain.Work {
		t.Errorf("業務の cwd: domain = %q", b.Domain)
	}
	if b := runStatusBoard(t, "--cwd", home, "--session", "W"); b.Domain != domain.Work {
		t.Errorf("業務に倒したセッション: domain = %q", b.Domain)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if b := runStatusBoard(t, "--cwd", home); b.Domain != domain.Work {
		t.Errorf("設定なし: domain = %q", b.Domain)
	}
}
