package control_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control"
)

// TestRealServer は実機の rex server に control 接続を張り、読み取りだけの method を呼ぶ。
// socket に繋がらないマシン (CI、rex の外) では skip する。
func TestRealServer(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("home が分からないので skip する: %v", err)
	}
	path, err := control.SocketPath(os.Getenv, home)
	if err != nil {
		t.Skipf("socket のパスが分からないので skip する: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := control.Dial(ctx, path, control.Options{ClientName: "agents-daemon-test", ClientVersion: "0"})
	if err != nil {
		t.Skipf("rex server に繋がらないので skip する: %v", err)
	}
	defer func() { _ = c.Close() }()
	if got := c.WelcomeRevision(); got != control.APIRevision {
		t.Errorf("welcome の api_revision = %d、名乗った %d のはず", got, control.APIRevision)
	}

	var st struct {
		PID int `json:"pid"`
	}
	if err := c.Call(ctx, "server.status", nil, &st); err != nil {
		t.Fatalf("server.status: %v", err)
	}
	if st.PID <= 0 {
		t.Errorf("server.status の pid = %d", st.PID)
	}
	if err := c.Call(ctx, "session.attach_all", map[string]any{"event_scope": "all"}, nil); err != nil {
		t.Fatalf("session.attach_all: %v", err)
	}
	var list struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := c.Call(ctx, "session.list", nil, &list); err != nil {
		t.Fatalf("session.list: %v", err)
	}
	if len(list.Sessions) == 0 {
		t.Skip("session が無いので以降を skip する")
	}
	session := list.Sessions[0].SessionID
	err = c.Call(ctx, "com.superlogical.terminal.program_status",
		map[string]any{"session_id": session, "block_id": "block:01a12599-0000-7000-8000-000000000001"}, nil)
	if code, _ := control.ErrorCode(err); code != "not_found" || !control.IsNotFound(err) {
		t.Errorf("無い block の error = %v、not_found のはず", err)
	}
}
