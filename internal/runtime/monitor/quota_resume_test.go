package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/quotaresume"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
)

// noticesReturning は sessionID によらず rec を返す ResumeNotices。
func noticesReturning(rec *quotaresume.Record) func(string) *quotaresume.Record {
	return func(string) *quotaresume.Record { return rec }
}

// quota_auto_resume_stale の通知は、画面の通知欄が読めなくても Enter だけを送る根拠になる (#529)。
func TestTick_staleの通知があれば通知欄が読めなくてもenterだけを送る(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	turn := limitedTurn(uuidLimit, now.Add(-time.Minute))
	var sentKeys []string
	client := &multiplexerfake.Client{
		PaneReadFunc: readReturning(cleanScreen, nil),
		SendKeysFunc: func(_ context.Context, _ string, keys ...string) error {
			sentKeys = append(sentKeys, keys...)
			return nil
		},
		SendTextFunc: func(context.Context, string, string) error {
			t.Error("SendText が呼ばれた: テキストを打つと中断された turn ではなく新しい依頼になる")
			return nil
		},
	}
	notice := &quotaresume.Record{SessionID: "sid-1", NotificationType: quotaresume.TypeStale, ObservedAt: now.Add(-10 * time.Second)}
	deps := newTestDeps(t, config.Default(), client, turn)
	deps.ResumeNotices = noticesReturning(notice)
	var observed Evidence
	deps.Observe = func(_ multiplexer.Pane, ev Evidence) { observed = ev }
	ps := waitingState(now, 0)

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeResumeEnter {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeResumeEnter)
	}
	if len(sentKeys) != 1 || sentKeys[0] != "enter" {
		t.Errorf("送ったキー = %v, want [enter]", sentKeys)
	}
	if observed.Hint != detect.HintResumeReady || observed.Notice != notice {
		t.Errorf("Evidence = hint %v notice %+v, want resume-ready と使った通知", observed.Hint, observed.Notice)
	}
}

// 前の上限の残りの stale (entry より前に届いた) と、fired / disabled の通知は根拠にしない。
func TestTick_今の上限に当たらない通知では通常の再開を送る(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	turn := limitedTurn(uuidLimit, now.Add(-time.Minute))
	tests := map[string]*quotaresume.Record{
		"entry より前の stale": {NotificationType: quotaresume.TypeStale, ObservedAt: turn.Entry.Timestamp.Add(-time.Second)},
		"fired":             {NotificationType: quotaresume.TypeFired, ObservedAt: now},
		"disabled":          {NotificationType: quotaresume.TypeDisabled, ObservedAt: now},
		"通知が無い":             nil,
	}
	for name, notice := range tests {
		t.Run(name, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
			deps := newTestDeps(t, config.Default(), client, turn)
			deps.ResumeNotices = noticesReturning(notice)
			var observed Evidence
			deps.Observe = func(_ multiplexer.Pane, ev Evidence) { observed = ev }
			ps := waitingState(now, 0)

			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

			if err != nil || outcome != OutcomeRetried {
				t.Errorf("Tick() = %v, %v, want retried", outcome, err)
			}
			if observed.Hint != detect.HintNone || observed.Notice != nil {
				t.Errorf("Evidence = hint %v notice %+v, want none / nil", observed.Hint, observed.Notice)
			}
		})
	}
}

// 本体が待っている画面 (予約中、リトライ中、lower-priority) は stale の通知より優先する。通知は通知欄が何も
// 言っていないときにだけ足す根拠。
func TestResolveHint_通知は通知欄が無いときだけ足す(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	entry := limitedTurn(uuidLimit, now.Add(-time.Minute)).Entry
	stale := &quotaresume.Record{NotificationType: quotaresume.TypeStale, ObservedAt: now}
	for _, screen := range []detect.Hint{detect.HintNone, detect.HintNativeRetrying, detect.HintNativeArmed, detect.HintResumeReady, detect.HintLowerPriority} {
		want := screen
		if screen == detect.HintNone {
			want = detect.HintResumeReady
		}
		if got := ResolveHint(screen, stale, entry); got != want {
			t.Errorf("ResolveHint(%v, stale) = %v, want %v", screen, got, want)
		}
		if got := ResolveHint(screen, nil, entry); got != screen {
			t.Errorf("ResolveHint(%v, nil) = %v, want %v", screen, got, screen)
		}
	}
}

// 画面を読めない tick は通知があっても送らない (読めないのは capture の失敗で、pane の様子が分からない)。
func TestTick_staleの通知があっても画面を読めなければ送らない(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning("", nil)}
	deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
	deps.ResumeNotices = noticesReturning(&quotaresume.Record{NotificationType: quotaresume.TypeStale, ObservedAt: now})
	ps := waitingState(now, 0)

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil || outcome != OutcomeReadError {
		t.Errorf("Tick() = %v, %v, want read-error", outcome, err)
	}
	assertNoSend(t, client)
}
