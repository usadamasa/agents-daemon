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

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/limitlog"
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

// 上限を検知して待機に入った tick は、根拠にした transcript の entry・record・block の高さと screen を 1 行で残す。
// 待機が続くだけの tick では書き直さない (daemon.log の重複除去と同じ遷移)。待機明けに送った tick は読んだ画面も残す。
func TestDaemonRuntimeTick_LimitEventRecorded(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	pane := claudePaneWithSession("p1", "sid-1", multiplexer.AgentStatusError)
	pane.AgentMessage = "You've hit your session limit"
	const screen = "⏺ You've hit your session limit · resets 9:30pm (Asia/Tokyo)\n❯ \n"
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) { return screen, nil },
		PaneGeometryFunc: func(context.Context, string) (multiplexer.Geometry, error) {
			return multiplexer.Geometry{Rows: 11, Columns: 139, AlternateScreen: true}, nil
		},
	}
	r, _ := newTestRuntime(t, client, now)
	writeTranscript(t, r, "sid-1", transcriptIdle, transcriptLimited)

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
	if ev.Transcript == nil || ev.Transcript.UUID != "cae11b0e-b253-403c-8cd6-7cf350809a97" || ev.Transcript.Error != "rate_limit" ||
		!strings.HasSuffix(ev.Transcript.Path, "sid-1.jsonl") {
		t.Errorf("transcript = %+v, want 根拠にした entry とパス", ev.Transcript)
	}
	if ev.Transcript.Quota == nil || ev.Transcript.Quota.RateLimitType != "five_hour" || ev.Transcript.Quota.ResetsAt == nil || !ev.Transcript.Quota.ResetsAt.Equal(transcriptResetsAt) {
		t.Errorf("quota_limits = %+v", ev.Transcript.Quota)
	}
	if ev.Screen != "" || ev.Hint != "" {
		t.Errorf("検知の tick で画面を残している: screen=%q hint=%q", ev.Screen, ev.Hint)
	}
	if ev.Record.AgentStatus != "error" || ev.Record.Msg != pane.AgentMessage {
		t.Errorf("record = %+v", ev.Record)
	}
	if ev.Geometry == nil || ev.Geometry.Rows != 11 || !ev.Geometry.AlternateScreen {
		t.Errorf("geometry = %+v", ev.Geometry)
	}
	if ev.WaitUntil == nil || !ev.WaitUntil.Equal(transcriptResetsAt.Add(60*time.Second)) {
		t.Errorf("wait_until = %v, want resetsAt + margin", ev.WaitUntil)
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

	// 待機明けに transcript がまだ同じ上限なら、画面を読んで送り、その画面と通知欄の判定を残す。
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), transcriptResetsAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("tick() 3 回目 error = %v", err)
	}
	events = readLimitEvents(t, r)
	if len(events) != 2 {
		t.Fatalf("events = %d 件, want 2", len(events))
	}
	if sent := events[1]; sent.Outcome != "retried" || sent.Screen != screen || sent.Hint != "none" || sent.Attempts != 1 {
		t.Errorf("送った出来事 = %+v", sent)
	}
	if !strings.Contains(strings.Join(client.Calls, " "), "SendText:p1") {
		t.Errorf("Calls = %v, want 文面を送っている", client.Calls)
	}
}

// 判定できなかった理由 (session_id が引けない、transcript が無い) と、transcript が上限なのに record が working の
// 食い違いも出来事として残す。影の期間を置かずに切り替えたので、これが前提の実測になる。
func TestDaemonRuntimeTick_LimitEventRecordsReasons(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	noSession := claudePane("p-none", multiplexer.AgentStatusIdle)
	noTranscript := claudePaneWithSession("p-missing", "sid-missing", multiplexer.AgentStatusIdle)
	working := claudePaneWithSession("p-working", "sid-working", multiplexer.AgentStatusWorking)
	quiet := claudePaneWithSession("p-quiet", "sid-quiet", multiplexer.AgentStatusIdle)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{noSession, noTranscript, working, quiet}, nil
		},
	}
	r, _ := newTestRuntime(t, client, now)
	writeTranscript(t, r, "sid-working", transcriptLimited)
	writeTranscript(t, r, "sid-quiet", transcriptIdle)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	byPane := map[string]limitlog.Event{}
	for _, ev := range readLimitEvents(t, r) {
		byPane[ev.PaneID] = ev
	}
	if len(byPane) != 3 {
		t.Fatalf("events = %v, want p-none / p-missing / p-working の 3 件", byPane)
	}
	if ev := byPane["p-none"]; ev.Outcome != "no-session" || !strings.Contains(ev.Reason, "session_id") || ev.Transcript != nil {
		t.Errorf("p-none = %+v", ev)
	}
	if ev := byPane["p-missing"]; ev.Outcome != "transcript-unavailable" || !strings.Contains(ev.Reason, "見つからない") {
		t.Errorf("p-missing = %+v", ev)
	}
	if ev := byPane["p-working"]; ev.Outcome != "suppressed-working" || ev.Kind != "limit" || ev.Transcript == nil || ev.Record.AgentStatus != "working" {
		t.Errorf("p-working = %+v", ev)
	}
	for _, call := range client.Calls {
		if strings.HasPrefix(call, "PaneRead") || strings.HasPrefix(call, "Send") {
			t.Errorf("判定できない pane を読んだか送った: %v", client.Calls)
		}
	}
}

// pane のセッションに StopFailure の記録があれば、出来事に turn_failure として添える。無ければ欄ごと出さない。
func TestDaemonRuntimeTick_LimitEventAttachesTurnFailure(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	panes := []multiplexer.Pane{
		claudePaneWithSession("p1", "sid-1", multiplexer.AgentStatusError),
		claudePaneWithSession("p2", "sid-2", multiplexer.AgentStatusError),
	}
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return panes, nil },
	}
	r, _ := newTestRuntime(t, client, now)
	writeTranscript(t, r, "sid-1", transcriptLimited)
	writeTranscript(t, r, "sid-2", transcriptLimited)
	failedAt := now.Add(-time.Minute)
	input := `{"session_id": "sid-1", "hook_event_name": "StopFailure", "error": "rate_limit",` +
		` "last_assistant_message": "You've hit your session limit"}`
	if err := r.turnFailures.Ingest([]byte(input), failedAt); err != nil {
		t.Fatal(err)
	}

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	events := readLimitEvents(t, r)
	if len(events) != 2 {
		t.Fatalf("events = %d 件, want 2: %+v", len(events), events)
	}
	byPane := map[string]limitlog.Event{}
	for _, ev := range events {
		byPane[ev.PaneID] = ev
	}
	got := byPane["p1"].TurnFailure
	if got == nil || got.Error != "rate_limit" || got.Message != "You've hit your session limit" || !got.ObservedAt.Equal(failedAt) {
		t.Errorf("p1 の turn_failure = %+v, want rate_limit at %v", got, failedAt)
	}
	if got := byPane["p2"].TurnFailure; got != nil {
		t.Errorf("p2 の turn_failure = %+v, want 無し (記録の無いセッション)", got)
	}
}

// geometry が取れなくても出来事は残し、理由を geometry_error に書く。普通に終わった turn では何も書かない。
func TestDaemonRuntimeTick_LimitEventWithoutGeometry(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	pane := claudePaneWithSession("p1", "sid-1", multiplexer.AgentStatusError)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneGeometryFunc: func(context.Context, string) (multiplexer.Geometry, error) {
			return multiplexer.Geometry{}, errors.New("rex [block call] が失敗")
		},
	}
	r, _ := newTestRuntime(t, client, now)

	writeTranscript(t, r, "sid-1", transcriptIdle)
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if events := readLimitEvents(t, r); len(events) != 0 {
		t.Fatalf("普通の turn で書いている: %+v", events)
	}

	writeTranscript(t, r, "sid-1", transcriptIdle, transcriptLimited)
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
