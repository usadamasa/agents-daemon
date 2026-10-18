package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// record の kind と msg は、あるときだけ pane の行に添える (上限で止まった pane の自己申告を status から読むため)。
func TestPrintSnapshot_record(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	snap := &daemon.StatusSnapshot{PID: 42, Backend: "rex", StartedAt: now, UpdatedAt: now, Panes: []daemon.PaneStatus{
		{PaneID: "p1", CWD: "/w", AgentStatus: "blocked", AgentKind: "permission", AgentMessage: "approve Bash: rm -rf ./tmp/junk", MonitorStatus: "waiting", LastTickAt: now},
		{PaneID: "p2", CWD: "/w", AgentStatus: "working", MonitorStatus: "monitoring", LastTickAt: now},
	}}
	var buf strings.Builder
	printSnapshot(&buf, snap, 42, now)
	out := buf.String()
	for _, want := range []string{`kind=permission`, `msg="approve Bash: rm -rf ./tmp/junk"`} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が無い:\n%s", want, out)
		}
	}
	if strings.Count(out, "kind=") != 1 {
		t.Errorf("kind の無い pane にも kind= が出ている:\n%s", out)
	}
}

func TestPrintSnapshot_backend(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		backend string
		want    string
	}{
		{"記録があれば表示する", "rex", "backend: rex"},
		{"変更前の daemon が書いた snapshot は記録なし", "", "backend: (記録なし)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			printSnapshot(&buf, &daemon.StatusSnapshot{PID: 42, Backend: tc.backend, StartedAt: now, UpdatedAt: now}, 42, now)
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("出力に %q が無い:\n%s", tc.want, buf.String())
			}
		})
	}
}
