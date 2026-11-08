package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
)

// waitingState は起床時刻に達した待機状態。
func waitingState(now time.Time, attempts int) *PaneState {
	return &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: attempts, TurnUUID: uuidLimit}
}

func TestTick_待機明けに本体が待っていれば送らず監視へ戻る(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		screen string
		want   Outcome
	}{
		// ここで Escape を送るとネイティブの継続そのものが取り消される ("esc or type to cancel")。
		{"auto-continue の予約", nativeArmedScreen, OutcomeNativeAutoContinue},
		// Escape は request の中の待機ごと取り消す。
		{"request の中のリトライ", nativeRetryingScreen, OutcomeNativeAutoContinue},
		// pane は低優先度で動いている (#251)。
		{"lower-priority mode", lowerPriorityScreen, OutcomeLowerPriority},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(tt.screen, nil)}
			deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
			ps := waitingState(now, 2)

			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != tt.want {
				t.Errorf("Outcome = %v, want %v", outcome, tt.want)
			}
			if *ps != (PaneState{}) {
				t.Errorf("ps = %+v, want ゼロ値 (本体に任せる間は待機しない)", ps)
			}
			assertNoSend(t, client)
		})
	}
}

func TestTick_deferToNativeAutoContinueをoffにすると予約中でも送る(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cfg := config.Default()
	cfg.DeferToNativeAutoContinue = false
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(nativeArmedScreen, nil)}
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
	ps := waitingState(now, 0)

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil || outcome != OutcomeRetried || ps.Attempts != 1 {
		t.Errorf("Tick() = %v, %v, ps = %+v; want retried", outcome, err, ps)
	}
}

func TestTick_リトライ中はdeferToNativeAutoContinueに従わない(t *testing.T) {
	// watchdog は auto-continue とは別の opt-in で、この設定を切っても待機を潰してよい理由にならない。
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cfg := config.Default()
	cfg.DeferToNativeAutoContinue = false
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(nativeRetryingScreen, nil)}
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
	ps := waitingState(now, 0)

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil || outcome != OutcomeNativeAutoContinue {
		t.Errorf("Tick() = %v, %v; want native-auto-continue", outcome, err)
	}
	assertNoSend(t, client)
}

func TestTick_enter待ちにはenterだけを送る(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	var sentKeys []string
	client := &multiplexerfake.Client{
		PaneReadFunc: readReturning(resumeReadyScreen, nil),
		SendKeysFunc: func(_ context.Context, _ string, keys ...string) error {
			sentKeys = append(sentKeys, keys...)
			return nil
		},
		SendTextFunc: func(context.Context, string, string) error {
			t.Error("SendText が呼ばれた: テキストを打つと中断された turn ではなく新しい依頼になる")
			return nil
		},
	}
	deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
	ps := waitingState(now, 0)

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeResumeEnter {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeResumeEnter)
	}
	if len(sentKeys) != 1 || sentKeys[0] != "enter" {
		t.Errorf("送ったキー = %v, want [enter] (esc はネイティブの取り消しキー)", sentKeys)
	}
	if ps.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (効かなかったときに maxRetries で打ち切るため)", ps.Attempts)
	}
}

func TestTick_enterが効かないままなら最終的に打ち切る(t *testing.T) {
	// Enter を押しても transcript が変わらないことがある。ここで数え上げが効かないと
	// 30 秒おきに永久に Enter を押し続ける pane ができる。
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(resumeReadyScreen, nil)}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
	ps := waitingState(now, 0)

	for range cfg.MaxRetries + 5 {
		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)
		if err != nil {
			t.Fatalf("Tick() error = %v", err)
		}
		if outcome == OutcomeMaxRetries {
			return
		}
		now = now.Add(time.Hour)
	}
	t.Errorf("maxRetries=%d を超えても打ち切られなかった (Attempts=%d)", cfg.MaxRetries, ps.Attempts)
}
