package main

import (
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
)

func TestPrintSnapshot_cache(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	last := now.Add(-18 * time.Minute)
	pane := func(c *daemon.CacheStatus) daemon.PaneStatus {
		return daemon.PaneStatus{PaneID: "pane-1", CWD: "/tmp/p", AgentStatus: "idle", MonitorStatus: "monitoring", LastOutcome: "monitoring", Cache: c}
	}
	snap := func(c *daemon.CacheStatus) *daemon.StatusSnapshot {
		return &daemon.StatusSnapshot{PID: 42, StartedAt: now.Add(-time.Hour), UpdatedAt: now, Panes: []daemon.PaneStatus{pane(c)}}
	}

	cases := []struct {
		name  string
		cache *daemon.CacheStatus
		want  string
	}{
		{"warm は失効までの残り分", &daemon.CacheStatus{State: "warm", LastRequestAt: last, TTLSeconds: 3600, ExpiresAt: last.Add(time.Hour)}, "cache=warm(残り 42 分)"},
		{"expired は失効からの経過分", &daemon.CacheStatus{State: "expired", LastRequestAt: last.Add(-5 * time.Hour), TTLSeconds: 3600, ExpiresAt: last.Add(-4 * time.Hour)}, "cache=expired(失効から 258 分)"},
		{"sidecar が無ければ -", nil, "cache=-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			printSnapshot(&buf, snap(tc.cache), 42, now)
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("出力に %q が無い:\n%s", tc.want, buf.String())
			}
		})
	}
}
