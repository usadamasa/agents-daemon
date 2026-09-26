package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
)

// compactStalledScreen は compact 直後に入力待ちで止まった画面。上限の文言は
// 一切出ないので、既存の二重シグナル検知にはかからない。
const compactStalledScreen = "⏺ 変更を適用しました。\n\n" +
	"✻ Conversation compacted (ctrl+o for history)\n\n" +
	"─────────\n❯\n─────────\n  🌸 Opus 5 ･ﾟ 🧠 41k (4%)\n"

// compactStallTick は quiet 期間を跨いで Tick を 2 回呼ぶ。1 回目は候補として
// 記録するだけ、2 回目で送信の判断に進む。
func compactStallTick(t *testing.T, cfg config.Config, client *herdrclifake.Client, pane herdrcli.Pane, ps *PaneState, start time.Time) Outcome {
	t.Helper()
	deps := newTestDeps(t, cfg, client)

	if _, err := Tick(context.Background(), deps, pane, ps, nil, start); err != nil {
		t.Fatalf("Tick() (1 回目) error = %v", err)
	}
	quiet := time.Duration(cfg.CompactStallQuietSeconds) * time.Second
	outcome, err := Tick(context.Background(), deps, pane, ps, nil, start.Add(quiet+time.Second))
	if err != nil {
		t.Fatalf("Tick() (2 回目) error = %v", err)
	}
	return outcome
}

func TestTick_compact直後に止まった画面へ継続を促す(t *testing.T) {
	var sentText string
	var sentKeys []string
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(compactStalledScreen, nil),
		SendTextFunc: func(_ context.Context, _, text string) error {
			sentText = text
			return nil
		},
		SendKeysFunc: func(_ context.Context, _ string, keys ...string) error {
			sentKeys = append(sentKeys, keys...)
			return nil
		},
	}
	cfg := config.Default()
	ps := &PaneState{}

	outcome := compactStallTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), ps, time.Now())
	if outcome != OutcomeCompactNudged {
		t.Fatalf("Outcome = %v, want %v", outcome, OutcomeCompactNudged)
	}
	if sentText != cfg.CompactStallMessage {
		t.Errorf("送ったテキスト = %q, want %q", sentText, cfg.CompactStallMessage)
	}
	// Escape は送らない。ここで開いている可能性のあるダイアログを勝手に閉じるより、
	// agent_status が idle であることを前提に素直にプロンプトへ流す。
	if len(sentKeys) != 1 || sentKeys[0] != "enter" {
		t.Errorf("送ったキー = %v, want [enter]", sentKeys)
	}
}

func TestTick_compact停止の検知には画面の安定を要求する(t *testing.T) {
	client := &herdrclifake.Client{PaneReadFunc: readReturning(compactStalledScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	// 初回の観測だけでは送らない (compact 直後に考え始めた Claude へ割り込まない)。
	outcome, err := Tick(context.Background(), deps, pane, ps, nil, time.Now())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeMonitoring {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_compact停止でも作業中のpaneには割り込まない(t *testing.T) {
	for _, status := range []herdrcli.AgentStatus{
		herdrcli.AgentStatusWorking,
		herdrcli.AgentStatusBlocked,
		herdrcli.AgentStatusUnknown,
	} {
		t.Run(string(status), func(t *testing.T) {
			client := &herdrclifake.Client{PaneReadFunc: readReturning(compactStalledScreen, nil)}
			ps := &PaneState{}

			outcome := compactStallTick(t, config.Default(), client, newTestPane(status, true), ps, time.Now())
			if outcome == OutcomeCompactNudged {
				t.Error("idle でない pane へ送信した")
			}
			assertNoSend(t, client)
		})
	}
}

func TestTick_compactStallEnabledがfalseなら何もしない(t *testing.T) {
	cfg := config.Default()
	cfg.CompactStallEnabled = false

	client := &herdrclifake.Client{PaneReadFunc: readReturning(compactStalledScreen, nil)}
	ps := &PaneState{}

	outcome := compactStallTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), ps, time.Now())
	if outcome != OutcomeMonitoring {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_compact停止の送信後はクールダウンを置く(t *testing.T) {
	client := &herdrclifake.Client{PaneReadFunc: readReturning(compactStalledScreen, nil)}
	cfg := config.Default()
	ps := &PaneState{}
	start := time.Now()

	if outcome := compactStallTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), ps, start); outcome != OutcomeCompactNudged {
		t.Fatalf("Outcome = %v, want %v", outcome, OutcomeCompactNudged)
	}

	// 送っても画面が変わらないことがある。同じ画面で何度も突かない。
	next := start.Add(time.Duration(cfg.CompactStallQuietSeconds)*time.Second + time.Second)
	if outcome := compactStallTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), ps, next); outcome == OutcomeCompactNudged {
		t.Error("クールダウン中なのに再送した")
	}
}

// compactStallRound は quiet と cooldown を跨いだ n 回目の停止判定を回し、
// その回の Outcome を返す。送っても画面が変わらないまま同じ停止が続く状況を再現する。
func compactStallRound(t *testing.T, cfg config.Config, client *herdrclifake.Client, ps *PaneState, start time.Time, n int) Outcome {
	t.Helper()
	period := time.Duration(cfg.CompactStallCooldownMinutes)*time.Minute +
		time.Duration(cfg.CompactStallQuietSeconds)*time.Second + 2*time.Second
	return compactStallTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), ps, start.Add(time.Duration(n)*period))
}

func TestTick_compact停止の継続要求は回数上限で止める(t *testing.T) {
	sent := 0
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(compactStalledScreen, nil),
		SendTextFunc: func(context.Context, string, string) error {
			sent++
			return nil
		},
	}
	cfg := config.Default()
	cfg.CompactStallMaxNudges = 2
	ps := &PaneState{}
	start := time.Now()

	for n := range cfg.CompactStallMaxNudges {
		if outcome := compactStallRound(t, cfg, client, ps, start, n); outcome != OutcomeCompactNudged {
			t.Fatalf("%d 回目: Outcome = %v, want %v", n+1, outcome, OutcomeCompactNudged)
		}
	}
	// 上限に達したら、cooldown が明けても同じ停止へは送らない。詰まった run は人が見る。
	outcome := compactStallRound(t, cfg, client, ps, start, cfg.CompactStallMaxNudges)
	if outcome != OutcomeCompactNudgeCapped {
		t.Errorf("上限後の Outcome = %v, want %v", outcome, OutcomeCompactNudgeCapped)
	}
	if sent != cfg.CompactStallMaxNudges {
		t.Errorf("送信回数 = %d, want %d", sent, cfg.CompactStallMaxNudges)
	}
}

func TestTick_停止が解けたら継続要求の回数を数え直す(t *testing.T) {
	screens := []string{compactStalledScreen}
	client := &herdrclifake.Client{
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return screens[len(screens)-1], nil
		},
	}
	cfg := config.Default()
	cfg.CompactStallMaxNudges = 1
	ps := &PaneState{}
	start := time.Now()

	if outcome := compactStallRound(t, cfg, client, ps, start, 0); outcome != OutcomeCompactNudged {
		t.Fatalf("1 回目: Outcome = %v, want %v", outcome, OutcomeCompactNudged)
	}
	if outcome := compactStallRound(t, cfg, client, ps, start, 1); outcome != OutcomeCompactNudgeCapped {
		t.Fatalf("2 回目: Outcome = %v, want %v", outcome, OutcomeCompactNudgeCapped)
	}

	// Claude が応答を流した (境界の後ろに ⏺ 行がある) ので停止は解けた。
	screens = append(screens, compactStalledScreen+"⏺ 続きを進めます。\n")
	deps := newTestDeps(t, cfg, client)
	if _, err := Tick(context.Background(), deps, newTestPane(herdrcli.AgentStatusIdle, true), ps, nil, start.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}

	// 次の compact で再び止まったら、新しい停止として数え直して送る。
	screens = append(screens, compactStalledScreen)
	if outcome := compactStallRound(t, cfg, client, ps, start.Add(2*time.Hour), 1); outcome != OutcomeCompactNudged {
		t.Errorf("停止が解けた後の Outcome = %v, want %v", outcome, OutcomeCompactNudged)
	}
}

func TestTick_画面が変われば安定の計測をやり直す(t *testing.T) {
	screens := []string{
		compactStalledScreen,
		strings.Replace(compactStalledScreen, "❯", "❯ 途中まで入力", 1),
	}
	i := 0
	client := &herdrclifake.Client{
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			s := screens[min(i, len(screens)-1)]
			i++
			return s, nil
		},
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}
	start := time.Now()

	if _, err := Tick(context.Background(), deps, pane, ps, nil, start); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	// 2 回目はユーザーが入力を始めているので候補から外れる。
	quiet := time.Duration(cfg.CompactStallQuietSeconds) * time.Second
	outcome, err := Tick(context.Background(), deps, pane, ps, nil, start.Add(quiet+time.Second))
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome == OutcomeCompactNudged {
		t.Error("入力途中の pane へ送信した")
	}
	assertNoSend(t, client)
}
