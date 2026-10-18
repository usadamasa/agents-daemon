package main

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

// --board の domain は config の homePaths と --cwd で決め、--session の記録が業務の側にあれば業務を描く。
// 設定が無ければ homePaths の下でも業務を描く。
func TestStatusBoard_domain(t *testing.T) {
	root := parkTestEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	work := filepath.Join(root, "work", "b")
	mkdirs(t, home, work)
	writeTaskRecord(t, root, "work", "W", `{"id": "1", "subject": "業務", "status": "pending", "session_id": "W"}`)

	if b := runStatusBoard(t, "--cwd", home); b.Domain != domain.Home {
		t.Errorf("私用の cwd: domain = %q", b.Domain)
	}
	if b := runStatusBoard(t, "--cwd", work); b.Domain != domain.Work {
		t.Errorf("業務の cwd: domain = %q", b.Domain)
	}
	if b := runStatusBoard(t, "--cwd", home, "--session", "W"); b.Domain != domain.Work {
		t.Errorf("業務に倒したセッション: domain = %q", b.Domain)
	}
	if err := os.Remove(filepath.Join(root, "config", "agents-daemon", "config.json")); err != nil {
		t.Fatal(err)
	}
	if b := runStatusBoard(t, "--cwd", home); b.Domain != domain.Work {
		t.Errorf("設定なし: domain = %q", b.Domain)
	}
}
