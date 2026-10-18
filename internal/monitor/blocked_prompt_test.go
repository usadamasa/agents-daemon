package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// blockedPane は Claude Code が「利用者の操作待ち」と申告した pane。
func blockedPane(kind, message string) multiplexer.Pane {
	pane := newTestPane(multiplexer.AgentStatusBlocked, true)
	pane.AgentKind = kind
	pane.AgentMessage = message
	return pane
}

// 待機明けに Claude Code が permission prompt などの dialog を開いていたら、文面も Enter も送らない。
// esc は prompt を断り、Enter は承認するので、どちらも利用者の判断を daemon が代行することになる。
// 限度の画面がこの 3 つの kind で来ることは無いので、再開を止める側には倒れない。
func TestTick_BlockedByPrompt_NoSend(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	screens := map[string]string{
		"上限の画面":          limitAndRelativeScreen,
		"解除済みで Enter 待ち": resumeReadyScreen,
	}
	for _, kind := range []string{"permission", "question", "auth"} {
		for screenName, screen := range screens {
			t.Run(kind+"/"+screenName, func(t *testing.T) {
				client := &multiplexerfake.Client{PaneReadFunc: readReturning(screen, nil)}
				cfg := config.Default()
				deps := newTestDeps(t, cfg, client)
				ps := &PaneState{Status: StatusWaiting, Attempts: 2}

				outcome, err := Tick(context.Background(), deps, blockedPane(kind, "approve Bash: rm"), ps, nil, now)

				if err != nil {
					t.Fatalf("Tick() error = %v", err)
				}
				if outcome != OutcomeBlockedByPrompt {
					t.Errorf("outcome = %v, want %v", outcome, OutcomeBlockedByPrompt)
				}
				if len(client.Calls) != 1 || client.Calls[0] != "PaneRead:pane-1" {
					t.Errorf("Calls = %v, want [PaneRead:pane-1] のみ (dialog が開いていれば送信しない)", client.Calls)
				}
				if ps.Status != StatusWaiting {
					t.Errorf("ps.Status = %v, want StatusWaiting (待機は続ける)", ps.Status)
				}
				if ps.Attempts != 2 {
					t.Errorf("ps.Attempts = %d, want 2 (送っていないので試行に数えない)", ps.Attempts)
				}
				want := now.Add(time.Duration(cfg.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
				if !ps.WaitUntil.Equal(want) {
					t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
				}
			})
		}
	}
}

// kind の無い blocked (Session paused などの dialog) は、上限の画面がこの形で来るかを実測していないので
// 今の挙動のまま送る (#77)。
func TestTick_BlockedWithoutKind_StillSends(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := Tick(context.Background(), deps, blockedPane("", ""), ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeRetried)
	}
	if len(client.Calls) != 4 {
		t.Errorf("Calls = %v, want PaneRead と esc / 文面 / enter の 4 回", client.Calls)
	}
}

// ゲートは送信側だけ。監視中の検知は画面だけで決め、dialog が開いていても上限の画面なら待機に入る
// (上限で止まった pane は agent_status からは区別できない。rate-limit.md)。
func TestTick_BlockedByPrompt_DetectionUnaffected(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, blockedPane("permission", "approve Bash: rm"), ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting || ps.Status != StatusWaiting {
		t.Errorf("outcome = %v, ps.Status = %v; want Waiting (検知は agent_status で止めない)", outcome, ps.Status)
	}
}
