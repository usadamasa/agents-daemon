package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// record があるのに使えない pane (rex が owner を付けなくなった等) は、理由を 1 回だけログに残す。
// 毎 tick 書くとログが埋まり、書かないと pane が unknown のままになった理由が追えない。
func TestDaemonRuntimeTick_UnknownReasonLoggedOnce(t *testing.T) {
	const reason = "record に owner.pid が無い (テスト)"
	pane := claudePane("p1", multiplexer.AgentStatusUnknown)
	pane.UnknownReason = reason
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{pane}, nil
		},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, client, now)

	for i := range 3 {
		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(time.Duration(i)*5*time.Second)); err != nil {
			t.Fatalf("tick() #%d error = %v", i+1, err)
		}
	}

	logBytes, err := os.ReadFile(filepath.Join(filepath.Dir(statusPath), "daemon.log"))
	if err != nil {
		t.Fatalf("daemon.log の読み込みに失敗: %v", err)
	}
	if n := strings.Count(string(logBytes), reason); n != 1 {
		t.Errorf("理由のログが %d 回 (want 1 回):\n%s", n, logBytes)
	}
	if !strings.Contains(string(logBytes), "pane p1") {
		t.Errorf("理由のログに pane ID が無い:\n%s", logBytes)
	}
}

// 置き場を読めない pane (rex の session.view の応答の形が変わった等) は、理由を 1 回だけログに残す。書かないと、
// 依存図で置き場が無いことが rex の変更で壊れたのか、置き場が本当に空なのかを見分けられない。
func TestDaemonRuntimeTick_LocationReasonLoggedOnce(t *testing.T) {
	const reason = "session.view の応答に windows が無い (テスト)"
	pane := claudePane("p1", multiplexer.AgentStatusWorking)
	pane.LocationReason = reason
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{pane}, nil
		},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, client, now)

	for i := range 3 {
		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(time.Duration(i)*5*time.Second)); err != nil {
			t.Fatalf("tick() #%d error = %v", i+1, err)
		}
	}

	logBytes, err := os.ReadFile(filepath.Join(filepath.Dir(statusPath), "daemon.log"))
	if err != nil {
		t.Fatalf("daemon.log の読み込みに失敗: %v", err)
	}
	if n := strings.Count(string(logBytes), reason); n != 1 {
		t.Errorf("理由のログが %d 回 (want 1 回):\n%s", n, logBytes)
	}
	if !strings.Contains(string(logBytes), "pane p1") {
		t.Errorf("理由のログに pane ID が無い:\n%s", logBytes)
	}
}
