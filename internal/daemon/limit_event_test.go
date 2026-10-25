package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// readLimitEvents は limit-events/ の全行を読む。無ければ nil。
func readLimitEvents(t *testing.T, r *daemonRuntime) []limitlog.Event {
	t.Helper()
	entries, err := os.ReadDir(r.limits.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("limit-events の走査に失敗: %v", err)
	}
	var out []limitlog.Event
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(r.limits.Dir, e.Name())) // #nosec G304 -- テストの一時ディレクトリ
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
			var ev limitlog.Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("%s の行を読めない: %v\n%s", e.Name(), err, line)
			}
			out = append(out, ev)
		}
	}
	return out
}

// 上限を検知して待機に入った tick は、判定した画面・record・block の高さと screen を 1 行で残す。
// 待機が続くだけの tick では書き直さない (daemon.log の重複除去と同じ遷移)。
func TestDaemonRuntimeTick_LimitEventRecorded(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pane := claudePane("p1", multiplexer.AgentStatusWorking)
	pane.SessionID = "sid-1"
	pane.AgentMessage = "長い作業"
	const screen = "line above\nTry again in 5 minutes\n❯ \n"
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) { return screen, nil },
		PaneGeometryFunc: func(context.Context, string) (multiplexer.Geometry, error) {
			return multiplexer.Geometry{Rows: 11, Columns: 139, AlternateScreen: true}, nil
		},
	}
	r, _ := newTestRuntime(t, client, now)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	events := readLimitEvents(t, r)
	if len(events) != 1 {
		t.Fatalf("events = %d 件, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Outcome != "waiting" || ev.Kind != "limit" || ev.PaneID != "p1" || ev.SessionID != "sid-1" {
		t.Errorf("event = %+v", ev)
	}
	if ev.Screen != screen {
		t.Errorf("screen = %q, want 判定した画面そのもの", ev.Screen)
	}
	if ev.Record.AgentStatus != "working" || ev.Record.Msg != "長い作業" {
		t.Errorf("record = %+v", ev.Record)
	}
	if ev.Geometry == nil || ev.Geometry.Rows != 11 || !ev.Geometry.AlternateScreen {
		t.Errorf("geometry = %+v", ev.Geometry)
	}
	if ev.WaitUntil == nil || !ev.WaitUntil.After(now) {
		t.Errorf("wait_until = %v, want 未来", ev.WaitUntil)
	}
	if ev.Settings.ReadLines == 0 || ev.Settings.DetectionTailLines == 0 {
		t.Errorf("settings = %+v, want config の値", ev.Settings)
	}
	if !strings.Contains(strings.Join(client.Calls, " "), "PaneGeometry:p1") {
		t.Errorf("Calls = %v, want PaneGeometry を打っている", client.Calls)
	}

	// 待機中の tick (起床前) では読まないし書かない。
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(5*time.Second)); err != nil {
		t.Fatalf("tick() 2 回目 error = %v", err)
	}
	if events := readLimitEvents(t, r); len(events) != 1 {
		t.Errorf("待機中の tick で書き足している: %d 件", len(events))
	}
}

// lower-priority mode の作業中の通知がある画面を上限と判定したときは、出来事に印を付け、daemon.log に 1 行残す (#251)。
// 判定と待機はまだ変えない。
func TestDaemonRuntimeTick_LimitEventMarksLowerPriority(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pane := claudePane("p1", multiplexer.AgentStatusWorking)
	const screen = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)\n\n" +
		"Continuing now at lower priority until your limit resets at 5pm. Your weekly limit still applies. Run /low-priority to stop.\n❯ \n"
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) { return screen, nil },
	}
	r, statusPath := newTestRuntime(t, client, now)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	events := readLimitEvents(t, r)
	if len(events) != 1 {
		t.Fatalf("events = %d 件, want 1: %+v", len(events), events)
	}
	if ev := events[0]; ev.Outcome != "waiting" || ev.Kind != "limit" || !ev.LowerPriority {
		t.Errorf("event = outcome %q kind %q lower_priority %v, want waiting / limit / true", ev.Outcome, ev.Kind, ev.LowerPriority)
	}
	if log := readLog(t, statusPath); !strings.Contains(log, "lower-priority mode") {
		t.Errorf("daemon.log に lower-priority mode の行が無い:\n%s", log)
	}
}

// 通常の上限の画面では印を付けず、daemon.log にも出さない。
func TestDaemonRuntimeTick_LimitEventWithoutLowerPriority(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pane := claudePane("p1", multiplexer.AgentStatusWorking)
	const screen = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)\n❯ \n"
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) { return screen, nil },
	}
	r, statusPath := newTestRuntime(t, client, now)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	events := readLimitEvents(t, r)
	if len(events) != 1 || events[0].LowerPriority {
		t.Fatalf("events = %+v, want lower_priority の無い 1 件", events)
	}
	if log := readLog(t, statusPath); strings.Contains(log, "lower-priority mode") {
		t.Errorf("通常の上限で lower-priority mode の行を出している:\n%s", log)
	}
}

// geometry が取れなくても出来事は残し、理由を geometry_error に書く。通常の画面では何も書かない。
func TestDaemonRuntimeTick_LimitEventWithoutGeometry(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pane := claudePane("p1", multiplexer.AgentStatusWorking)
	screen := "Try again in 5 minutes"
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) { return screen, nil },
		PaneGeometryFunc: func(context.Context, string) (multiplexer.Geometry, error) {
			return multiplexer.Geometry{}, errors.New("rex [block call] が失敗")
		},
	}
	r, _ := newTestRuntime(t, client, now)

	screen = "$ normal output"
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if events := readLimitEvents(t, r); len(events) != 0 {
		t.Fatalf("通常の画面で書いている: %+v", events)
	}

	screen = "Try again in 5 minutes"
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(5*time.Second)); err != nil {
		t.Fatalf("tick() 2 回目 error = %v", err)
	}
	events := readLimitEvents(t, r)
	if len(events) != 1 {
		t.Fatalf("events = %d 件, want 1", len(events))
	}
	if events[0].Geometry != nil || !strings.Contains(events[0].GeometryError, "block call") {
		t.Errorf("geometry = %+v, geometry_error = %q", events[0].Geometry, events[0].GeometryError)
	}
}
