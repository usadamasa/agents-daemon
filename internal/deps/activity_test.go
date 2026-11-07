package deps

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/worklog"
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

// pane の写しより後に prompt を受け、まだ Stop していないセッションは、写しが idle や done でも作業中 (#469)。
// 写しは daemon の tick ごとにしか新しくならず、UserPromptSubmit の記録はそれより先に届く。
func TestActivities_写しより新しいpromptは作業中(t *testing.T) {
	tick := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := tick.Add(d); return &v }
	for _, c := range []struct {
		name string
		pane daemonstate.PaneStatus
		work *worklog.Record
		want string
	}{
		{"写しの後の prompt で idle", daemonstate.PaneStatus{AgentStatus: "idle", LastTickAt: tick},
			&worklog.Record{PromptedAt: at(time.Second)}, graph.StateWorking},
		{"写しの後の prompt で done", daemonstate.PaneStatus{AgentStatus: "done", LastTickAt: tick},
			&worklog.Record{PromptedAt: at(time.Second), StoppedAt: at(-time.Minute)}, graph.StateWorking},
		{"prompt の後に Stop した", daemonstate.PaneStatus{AgentStatus: "idle", LastTickAt: tick},
			&worklog.Record{PromptedAt: at(time.Second), StoppedAt: at(2 * time.Second)}, graph.StateDone},
		{"prompt の後の写しで idle (Esc で止めると Stop は来ない)", daemonstate.PaneStatus{AgentStatus: "idle", LastTickAt: tick},
			&worklog.Record{PromptedAt: at(-time.Second)}, graph.StateDone},
		{"作業の記録が無い", daemonstate.PaneStatus{AgentStatus: "idle", LastTickAt: tick}, nil, graph.StateDone},
		{"prompt の時刻が無い記録", daemonstate.PaneStatus{AgentStatus: "idle", LastTickAt: tick}, &worklog.Record{}, graph.StateDone},
		{"人の手を待つ申告は上書きしない", daemonstate.PaneStatus{AgentStatus: "blocked", AgentKind: "question", LastTickAt: tick},
			&worklog.Record{PromptedAt: at(time.Second)}, graph.StateQuestion},
		{"上限待ちは上書きしない", daemonstate.PaneStatus{AgentStatus: "idle", MonitorStatus: "waiting", LastTickAt: tick},
			&worklog.Record{PromptedAt: at(time.Second)}, graph.StateLimit},
	} {
		var works []worklog.Record
		if c.work != nil {
			w := *c.work
			w.SessionID = "s1"
			works = append(works, w)
		}
		got := activities(map[string]*daemonstate.PaneStatus{"s1": &c.pane}, works)["s1"].act
		if got != c.want {
			t.Errorf("%s: act = %q, want %q", c.name, got, c.want)
		}
	}
}
