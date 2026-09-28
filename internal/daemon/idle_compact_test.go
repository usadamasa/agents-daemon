package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/monitor"
)

// idleCompactConfigPath は idle compact を有効にした config.json を置く｡
func idleCompactConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"cacheIdleCompactEnabled": true}`), 0o600); err != nil {
		t.Fatalf("config.json の書き込みに失敗: %v", err)
	}
	return path
}

func TestDaemonRuntimeTick_IdleCompactMarker(t *testing.T) {
	last := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
	now := last.Add(50 * time.Minute)
	pane := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return []herdrcli.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return "⏺ 変更を適用しました｡\n\n─────────\n❯\n─────────\n", nil
		},
	}

	// prepPending は idle 起点で compact-prep を送り､state file が書かれた後の状態を作る｡
	prepPending := func(t *testing.T, r *daemonRuntime) {
		t.Helper()
		writeCacheSidecar(t, r.store.Cache, "session-a")
		prep := filepath.Join(r.store.CompactState, "session-a.md")
		if err := os.MkdirAll(r.store.CompactState, 0o755); err != nil {
			t.Fatalf("compact-state ディレクトリの作成に失敗: %v", err)
		}
		if err := os.WriteFile(prep, []byte("# state\n"), 0o644); err != nil {
			t.Fatalf("state file の書き込みに失敗: %v", err)
		}
		if err := os.Chtimes(prep, now.Add(-10*time.Second), now.Add(-10*time.Second)); err != nil {
			t.Fatalf("state file の mtime 変更に失敗: %v", err)
		}
		r.panes["term-a"] = &paneEntry{state: &monitor.PaneState{
			CompactPrepSentAt: now.Add(-40 * time.Second),
			CompactAutoSentAt: now.Add(-40 * time.Second),
			CompactPrepIdle:   true,
		}}
	}

	t.Run("/compact の前に marker を書き､ログに残す", func(t *testing.T) {
		r, statusPath := newTestRuntime(t, client, now)
		prepPending(t, r)

		if _, _, err := r.tick(context.Background(), idleCompactConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.store.IdleCompacted, "session-a")); err != nil {
			t.Errorf("idle compact の marker が書かれていない: %v", err)
		}
		if log := readLog(t, statusPath); !strings.Contains(log, "idle compact") || !strings.Contains(log, "cache=warm") {
			t.Errorf("ログに idle compact の送信と cache の状態が無い:\n%s", log)
		}
	})

	t.Run("dry-run では marker を書かない", func(t *testing.T) {
		r, _ := newTestRuntime(t, client, now)
		r.dryRun = true
		prepPending(t, r)

		if _, _, err := r.tick(context.Background(), idleCompactConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.store.IdleCompacted, "session-a")); !os.IsNotExist(err) {
			t.Errorf("dry-run なのに marker が書かれている (err = %v)", err)
		}
	})
}
