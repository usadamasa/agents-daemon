package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeBoard(t *testing.T, out string) Board {
	t.Helper()
	var b Board
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("JSON として読めない: %v\n%s", err, out)
	}
	return b
}

// daemon が動いていなくてもエラーにせず、空のボードを出す (mod が「daemon が動いていない」と描けるように)。
func TestWrite_daemonNotRunning(t *testing.T) {
	p := apppath.New(t.TempDir(), "", "")
	var buf strings.Builder
	if err := Write(&buf, p); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if b := decodeBoard(t, buf.String()); b.DaemonRunning {
		t.Errorf("daemon_running = true, want false:\n%s", buf.String())
	}
}

// 生きている pid の status.json を、session_id ごとカードに写す。
func TestWrite_readsStatusSnapshot(t *testing.T) {
	p := apppath.New(t.TempDir(), "", "")
	pid := os.Getpid()
	writeTestFile(t, p.PIDFile(), strconv.Itoa(pid))
	writeTestFile(t, p.StatusFile(), `{"pid":`+strconv.Itoa(pid)+`,"updated_at":"2026-10-18T09:00:00Z","panes":[`+
		`{"pane_id":"p1","session_id":"s1","cwd":"/src/agents-daemon","agent_status":"blocked","monitor_status":"monitoring"}]}`)

	var buf strings.Builder
	if err := Write(&buf, p); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	b := decodeBoard(t, buf.String())
	if !b.DaemonRunning || b.Lanes[0].ID != LaneBlocked || len(b.Lanes[0].Cards) != 1 {
		t.Fatalf("board = %s", buf.String())
	}
	if c := b.Lanes[0].Cards[0]; c.SessionID != "s1" || c.Project != "agents-daemon" {
		t.Errorf("card = %+v", c)
	}
}
