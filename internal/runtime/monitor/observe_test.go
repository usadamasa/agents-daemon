package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
)

// Deps.Observe は判定の根拠 (読んだ transcript の末尾と分類、待機明けに読んだ画面と通知欄の判定) を受け取る。
// daemon が上限の出来事の記録に写すための口で、判定には関わらない。
func TestTick_ObserveReceivesEvidence(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	turn := limitedTurn(uuidLimit, now.Add(-time.Minute))

	t.Run("検知時は transcript だけ", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, config.Default(), client, turn)
		var got Evidence
		calls := 0
		deps.Observe = func(_ multiplexer.Pane, ev Evidence) { got, calls = ev, calls+1 }

		if _, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), &PaneState{}, now); err != nil {
			t.Fatalf("Tick() error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("Observe の呼び出し = %d 回, want 1", calls)
		}
		if got.Turn.Entry == nil || got.Turn.Entry.UUID != uuidLimit || got.Turn.Path != turn.Path || got.Kind != detect.KindLimit {
			t.Errorf("Evidence = %+v, want 読んだ transcript と limit", got)
		}
		if got.ScreenRead || got.Screen != "" {
			t.Errorf("ScreenRead = %v Screen = %q, want 検知時は画面を読まない", got.ScreenRead, got.Screen)
		}
	})

	t.Run("待機明けは画面と通知欄の判定も", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(resumeReadyScreen, nil)}
		deps := newTestDeps(t, config.Default(), client, turn)
		var got Evidence
		deps.Observe = func(_ multiplexer.Pane, ev Evidence) { got = ev }

		if _, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), waitingState(now, 0), now); err != nil {
			t.Fatalf("Tick() error = %v", err)
		}
		if !got.ScreenRead || got.Screen != resumeReadyScreen || got.Hint != detect.HintResumeReady {
			t.Errorf("Evidence = %+v, want 画面と resume-ready", got)
		}
	})

	t.Run("読めない transcript は理由ごと渡す", func(t *testing.T) {
		deps := newTestDeps(t, config.Default(), &multiplexerfake.Client{}, unreadableTurn)
		var got Evidence
		deps.Observe = func(_ multiplexer.Pane, ev Evidence) { got = ev }

		if _, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), &PaneState{}, now); err != nil {
			t.Fatalf("Tick() error = %v", err)
		}
		if got.Turn.Reason != unreadableTurn.Reason || got.Kind != detect.KindNone {
			t.Errorf("Evidence = %+v, want 理由", got)
		}
	})
}

// 起床前の tick では transcript を読まないので Observe も呼ばない。
func TestTick_ObserveNotCalledBeforeWake(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	calls := 0
	deps := newTestDeps(t, config.Default(), &multiplexerfake.Client{}, limitedTurn(uuidLimit, now.Add(time.Hour)))
	deps.Observe = func(multiplexer.Pane, Evidence) { calls++ }

	if _, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true),
		&PaneState{Status: StatusWaiting, WaitUntil: now.Add(time.Hour)}, now); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if calls != 0 {
		t.Errorf("Observe の呼び出し = %d 回, want 0", calls)
	}
}
