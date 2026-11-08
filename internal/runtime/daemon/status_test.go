package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
	"github.com/usadamasa/agents-daemon/internal/transcript"
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
	snap, err := daemonstate.ReadStatusSnapshot(statusPath)
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
	got := paneStatus(multiplexer.Pane{PaneID: "p1", SessionID: "s1"}, &paneEntry{state: &monitor.PaneState{}}, 0, nil, now)
	if got.SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1", got.SessionID)
	}
}

// 上限を判定できない理由 (session_id が引けない、transcript が読めない) は status.json に写し、doctor が出す。
func TestPaneStatus_detectReason(t *testing.T) {
	now := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	entry := &paneEntry{state: &monitor.PaneState{}}
	unavailable := &monitor.Evidence{Turn: monitor.Turn{Reason: "transcript が見つからない"}}
	if got := paneStatus(multiplexer.Pane{PaneID: "p1"}, entry, monitor.OutcomeTranscriptUnavailable, unavailable, now); got.DetectReason != "transcript が見つからない" {
		t.Errorf("DetectReason = %q", got.DetectReason)
	}
	readable := &monitor.Evidence{Turn: monitor.Turn{Path: "/t.jsonl", Entry: &transcript.Entry{Type: "user"}}}
	if got := paneStatus(multiplexer.Pane{PaneID: "p1"}, entry, monitor.OutcomeMonitoring, readable, now); got.DetectReason != "" {
		t.Errorf("DetectReason = %q, want 空 (判定できている)", got.DetectReason)
	}
	if got := paneStatus(multiplexer.Pane{PaneID: "p1"}, entry, monitor.OutcomeStillWaiting, nil, now); got.DetectReason != "" {
		t.Errorf("DetectReason = %q, want 空 (読まなかった tick)", got.DetectReason)
	}
}
