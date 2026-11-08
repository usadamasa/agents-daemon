package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
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
func TestTick_BlockedByPrompt_NoSend(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	for _, kind := range []string{"permission", "question", "auth"} {
		t.Run(kind, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
			cfg := config.Default()
			deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
			ps := waitingState(now, 2)

			outcome, err := Tick(context.Background(), deps, blockedPane(kind, "approve Bash: rm"), ps, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != OutcomeBlockedByPrompt {
				t.Errorf("outcome = %v, want %v", outcome, OutcomeBlockedByPrompt)
			}
			assertNoRead(t, client)
			if ps.Status != StatusWaiting || ps.Attempts != 2 {
				t.Errorf("ps = %+v, want 待機を続け、送っていないので試行に数えない", ps)
			}
			want := now.Add(time.Duration(cfg.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
			if !ps.WaitUntil.Equal(want) {
				t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
			}
		})
	}
}

// kind の無い blocked (Session paused などの dialog) は、上限の画面がこの形で来るかを実測していないので送る。
func TestTick_BlockedWithoutKind_StillSends(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
	deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(-time.Minute)))
	ps := waitingState(now, 0)

	outcome, err := Tick(context.Background(), deps, blockedPane("", ""), ps, now)

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

// prompt が開いていても検知は transcript で決まり、待機には入る。送信だけを止める。
func TestTick_BlockedByPrompt_DetectionUnaffected(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{}
	deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(time.Hour)))
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, blockedPane("permission", "approve Bash: rm"), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting || ps.Status != StatusWaiting {
		t.Errorf("outcome = %v, ps.Status = %v; want Waiting", outcome, ps.Status)
	}
}
