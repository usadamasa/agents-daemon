package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon/state"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

func TestDaemonRuntimeTick_StatusRecordsBackend(t *testing.T) {
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, client, now)
	r.backend = "rex"

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	snap, err := state.ReadStatusSnapshot(statusPath)
	if err != nil || snap == nil {
		t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
	}
	if snap.Backend != "rex" {
		t.Errorf("snap.Backend = %q, want rex", snap.Backend)
	}
}

// session_id は依存図がタスクとセッションを突き合わせる鍵で、指示を送る宛先にもなるので、pane から status.json へ写す。
func TestPaneStatus_sessionID(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	got := paneStatus(multiplexer.Pane{PaneID: "p1", SessionID: "s1"}, &paneEntry{state: &monitor.PaneState{}}, 0, now)
	if got.SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1", got.SessionID)
	}
}
