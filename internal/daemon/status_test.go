package daemon

import (
	"context"
	"testing"
	"time"

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
	snap, err := ReadStatusSnapshot(statusPath)
	if err != nil || snap == nil {
		t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
	}
	if snap.Backend != "rex" {
		t.Errorf("snap.Backend = %q, want rex", snap.Backend)
	}
}
