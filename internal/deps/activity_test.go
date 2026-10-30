package deps

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
)

// セッションの活動は pane から 1 か所で導く。上限待ちは agent の申告より先に見る。
func TestActivity(t *testing.T) {
	until := time.Date(2026, 10, 18, 14, 30, 0, 0, time.Local)
	for _, c := range []struct {
		name   string
		pane   daemonstate.PaneStatus
		want   string
		detail string
	}{
		{"作業中", daemonstate.PaneStatus{AgentStatus: "working"}, graph.StateWorking, ""},
		{"ターンを終えた", daemonstate.PaneStatus{AgentStatus: "done"}, graph.StateDone, ""},
		{"idle", daemonstate.PaneStatus{AgentStatus: "idle"}, graph.StateDone, ""},
		{"質問", daemonstate.PaneStatus{AgentStatus: "blocked", AgentKind: "question"}, graph.StateQuestion, ""},
		{"permission", daemonstate.PaneStatus{AgentStatus: "blocked", AgentKind: "permission"}, graph.StatePermission, ""},
		{"auth は permission に寄せる", daemonstate.PaneStatus{AgentStatus: "blocked", AgentKind: "auth"}, graph.StatePermission, ""},
		{"kind の無い blocked", daemonstate.PaneStatus{AgentStatus: "blocked"}, graph.StatePermission, ""},
		{"失敗", daemonstate.PaneStatus{AgentStatus: "error"}, graph.StateError, ""},
		{"申告の前", daemonstate.PaneStatus{}, graph.StateUnknown, ""},
		{"知らない語", daemonstate.PaneStatus{AgentStatus: "unknown"}, graph.StateUnknown, ""},
		{"上限待ち", daemonstate.PaneStatus{AgentStatus: "error", MonitorStatus: "waiting", WaitUntil: &until}, graph.StateLimit, "14:30"},
		{"解除時刻の無い上限待ち", daemonstate.PaneStatus{AgentStatus: "working", MonitorStatus: "waiting"}, graph.StateLimit, ""},
	} {
		got, detail := activity(&c.pane)
		if got != c.want || detail != c.detail {
			t.Errorf("%s: activity = %q / %q, want %q / %q", c.name, got, detail, c.want, c.detail)
		}
	}
}
