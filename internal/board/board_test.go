package board

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
)

func laneCards(b Board, id string) []Card {
	for _, l := range b.Lanes {
		if l.ID == id {
			return l.Cards
		}
	}
	return nil
}

func TestBuild_lanes(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	wait := now.Add(time.Hour)
	snap := &daemon.StatusSnapshot{PID: 42, UpdatedAt: now, Panes: []daemon.PaneStatus{
		{PaneID: "p1", SessionID: "s1", CWD: "/src/b", AgentStatus: "blocked", AgentKind: "permission", AgentMessage: "approve Bash", MonitorStatus: "monitoring"},
		{PaneID: "p2", CWD: "/src/a", AgentStatus: "idle", MonitorStatus: "waiting", WaitUntil: &wait},
		{PaneID: "p3", CWD: "/src/c", AgentStatus: "working", MonitorStatus: "monitoring"},
		{PaneID: "p4", CWD: "/src/d", AgentStatus: "done", MonitorStatus: "monitoring"},
		{PaneID: "p5", CWD: "/src/e", AgentStatus: "unknown", MonitorStatus: "monitoring"},
	}}

	b := Build(42, snap)

	if !b.DaemonRunning || b.UpdatedAt == nil || !b.UpdatedAt.Equal(now) {
		t.Fatalf("DaemonRunning/UpdatedAt = %v/%v", b.DaemonRunning, b.UpdatedAt)
	}
	cases := []struct {
		lane string
		pane string
	}{
		{LaneBlocked, "p1"},
		// 上限待ちは agent の申告 (idle) より先に見る。
		{LaneWaiting, "p2"},
		{LaneWorking, "p3"},
		{LaneIdle, "p4"},
		{LaneOther, "p5"},
	}
	for _, tc := range cases {
		cards := laneCards(b, tc.lane)
		if len(cards) != 1 || cards[0].PaneID != tc.pane {
			t.Errorf("lane %s = %+v, want %s だけ", tc.lane, cards, tc.pane)
		}
	}
	blocked := laneCards(b, LaneBlocked)[0]
	if blocked.SessionID != "s1" || blocked.Project != "b" || blocked.AgentKind != "permission" || blocked.Message != "approve Bash" {
		t.Errorf("blocked card = %+v", blocked)
	}
	if got := laneCards(b, LaneWaiting)[0].WaitUntil; got == nil || !got.Equal(wait) {
		t.Errorf("waiting card WaitUntil = %v, want %v", got, wait)
	}
}

func TestBuild_laneOrderIsFixed(t *testing.T) {
	b := Build(0, nil)
	var ids []string
	for _, l := range b.Lanes {
		ids = append(ids, l.ID)
		if l.Cards == nil {
			t.Errorf("lane %s の Cards が nil (JSON で null になり、mod が空配列として扱えない)", l.ID)
		}
	}
	want := []string{LaneBlocked, LaneWaiting, LaneWorking, LaneIdle, LaneOther}
	if len(ids) != len(want) {
		t.Fatalf("lanes = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("lanes = %v, want %v", ids, want)
		}
	}
}

func TestBuild_daemonState(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	panes := []daemon.PaneStatus{{PaneID: "p1", AgentStatus: "working"}}
	cases := []struct {
		name        string
		pid         int
		snap        *daemon.StatusSnapshot
		wantRunning bool
	}{
		{"daemon が動いていない", 0, &daemon.StatusSnapshot{PID: 42, UpdatedAt: now, Panes: panes}, false},
		{"起動直後でスナップショットがまだ無い", 42, nil, true},
		{"前の daemon が残したスナップショットは使わない", 42, &daemon.StatusSnapshot{PID: 7, UpdatedAt: now, Panes: panes}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := Build(tc.pid, tc.snap)
			if b.DaemonRunning != tc.wantRunning {
				t.Errorf("DaemonRunning = %v, want %v", b.DaemonRunning, tc.wantRunning)
			}
			if b.UpdatedAt != nil {
				t.Errorf("UpdatedAt = %v, want nil", b.UpdatedAt)
			}
			for _, l := range b.Lanes {
				if len(l.Cards) != 0 {
					t.Errorf("lane %s にカードがある: %+v", l.ID, l.Cards)
				}
			}
		})
	}
}

func TestBuild_cardOrder(t *testing.T) {
	snap := &daemon.StatusSnapshot{PID: 1, Panes: []daemon.PaneStatus{
		{PaneID: "p9", CWD: "/src/zeta", AgentStatus: "working"},
		{PaneID: "p2", CWD: "/src/alpha", AgentStatus: "working"},
		{PaneID: "p1", CWD: "/src/alpha", AgentStatus: "working"},
	}}
	cards := laneCards(Build(1, snap), LaneWorking)
	var got []string
	for _, c := range cards {
		got = append(got, c.PaneID)
	}
	want := []string{"p1", "p2", "p9"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
