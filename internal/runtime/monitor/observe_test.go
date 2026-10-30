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

// Deps.Observe は画面を読んで分類するたびに、分類した画面そのものと kind を受け取る。
// daemon が上限の出来事の記録に「判定した画面」を添えるための口で、判定には関わらない。
func TestTick_ObserveReceivesClassifiedScreen(t *testing.T) {
	tests := []struct {
		name     string
		screen   string
		wantKind detect.Kind
	}{
		{"limit の画面", limitAndRelativeScreen, detect.KindLimit},
		{"通常の画面", cleanScreen, detect.KindNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(tt.screen, nil)}
			deps := newTestDeps(t, config.Default(), client)
			var got struct {
				pane   multiplexer.Pane
				kind   detect.Kind
				screen string
				calls  int
			}
			deps.Observe = func(pane multiplexer.Pane, kind detect.Kind, screen string) {
				got.pane, got.kind, got.screen = pane, kind, screen
				got.calls++
			}
			pane := newTestPane(multiplexer.AgentStatusWorking, true)

			if _, err := Tick(context.Background(), deps, pane, &PaneState{}, nil, time.Now()); err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if got.calls != 1 {
				t.Fatalf("Observe の呼び出し = %d 回, want 1", got.calls)
			}
			if got.pane.PaneID != pane.PaneID || got.kind != tt.wantKind || got.screen != tt.screen {
				t.Errorf("Observe(%q, %v, %q), want (%q, %v, %q)", got.pane.PaneID, got.kind, got.screen, pane.PaneID, tt.wantKind, tt.screen)
			}
		})
	}
}

// 画面を読まなかった tick (起床前、読み取り失敗) では Observe を呼ばない。
func TestTick_ObserveNotCalledWithoutScreen(t *testing.T) {
	calls := 0
	observe := func(multiplexer.Pane, detect.Kind, string) { calls++ }
	now := time.Now()

	client := &multiplexerfake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	deps.Observe = observe
	if _, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true),
		&PaneState{Status: StatusWaiting, WaitUntil: now.Add(time.Hour)}, nil, now); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}

	failing := &multiplexerfake.Client{PaneReadFunc: readReturning("", context.DeadlineExceeded)}
	deps = newTestDeps(t, config.Default(), failing)
	deps.Observe = observe
	if _, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), &PaneState{}, nil, now); err == nil {
		t.Fatal("読み取り失敗で error が無い")
	}

	if calls != 0 {
		t.Errorf("Observe の呼び出し = %d 回, want 0", calls)
	}
}
