package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

const (
	// idleScreen は入力欄が空で､上限にも transient にも該当しない画面｡
	idleScreen = "⏺ 変更を適用しました｡\n\n─────────\n❯\n─────────\n  🌸 Opus 5 ･ﾟ 🧠 620k (62%)\n"
	// typingScreen は入力欄に書きかけのテキストが残っている画面｡
	typingScreen = "⏺ 変更を適用しました｡\n\n─────────\n❯ 次はこれを\n─────────\n  🌸 Opus 5 ･ﾟ 🧠 620k (62%)\n"
)

// compactAutoConfig は 1 段目・2 段目を有効にした設定を返す｡
func compactAutoConfig() config.Config {
	cfg := config.Default()
	cfg.CompactAutoEnabled = true
	// 各テストの使用率 62 はこの閾値に対する値｡既定値の変更に引きずられないよう固定する｡
	cfg.CompactAutoThresholdPercent = 60
	// 画面判定の fallback は切る｡ここで見たいのは marker / state file 経路だけ｡
	cfg.CompactStallEnabled = false
	return cfg
}

// compactTick は compact の段を 1 tick 回す｡
func compactTick(t *testing.T, cfg config.Config, client *herdrclifake.Client, pane herdrcli.Pane, ps *PaneState, cs *sessionstate.Compact, now time.Time) Outcome {
	t.Helper()
	deps := newTestDeps(t, cfg, client)
	deps.CompactState = func(string) *sessionstate.Compact { return cs }
	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	return outcome
}

// freshContext は使用率 pct を now に観測した context state を返す｡
func freshContext(pct float64, now time.Time) *sessionstate.Compact {
	return &sessionstate.Compact{HasContext: true, UsedPercentage: pct, ObservedAt: now}
}

func TestTick_使用率が閾値を超えたらcompactPrepを投入する(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}
	cfg := compactAutoConfig()

	outcome := compactTick(t, cfg, client, pane, ps, freshContext(62, now), now)

	if outcome != OutcomeCompactPrepSent {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactPrepSent)
	}
	assertSentText(t, client, cfg.CompactAutoPrepMessage)
	if ps.CompactPrepSentAt != now {
		t.Errorf("CompactPrepSentAt = %v, want %v", ps.CompactPrepSentAt, now)
	}
}

func TestTick_使用率が閾値未満なら何もしない(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	ps := &PaneState{}

	outcome := compactTick(t, compactAutoConfig(), client, newTestPane(herdrcli.AgentStatusIdle, true), ps, freshContext(59, now), now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_使用率の観測が古いなら何もしない(t *testing.T) {
	// statusline の描画が止まっている pane は､そのセッションが生きていない｡
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	cfg := compactAutoConfig()
	stale := freshContext(80, now.Add(-time.Duration(cfg.StateMaxAgeSeconds+1)*time.Second))

	outcome := compactTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), &PaneState{}, stale, now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_入力欄に書きかけがあるなら送らない(t *testing.T) {
	// Escape は入力行をクリアしないため､残った文字に送信文字列が連結される｡
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(typingScreen, nil)}

	outcome := compactTick(t, compactAutoConfig(), client, newTestPane(herdrcli.AgentStatusIdle, true), &PaneState{}, freshContext(80, now), now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_作業中のpaneには投入しない(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, status := range []herdrcli.AgentStatus{herdrcli.AgentStatusWorking, herdrcli.AgentStatusBlocked, ""} {
		client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}

		outcome := compactTick(t, compactAutoConfig(), client, newTestPane(status, true), &PaneState{}, freshContext(80, now), now)

		if outcome != OutcomeMonitoring {
			t.Errorf("status=%q: outcome = %v, want %v", status, outcome, OutcomeMonitoring)
		}
		assertNoSend(t, client)
	}
}

func TestTick_compactAutoEnabledがfalseなら何もしない(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	cfg := compactAutoConfig()
	cfg.CompactAutoEnabled = false

	outcome := compactTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), &PaneState{}, freshContext(95, now), now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_stateFileが投入後に書かれたらcompactを投入する(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}
	cfg := compactAutoConfig()

	if outcome := compactTick(t, cfg, client, pane, ps, freshContext(62, now), now); outcome != OutcomeCompactPrepSent {
		t.Fatalf("1 段目: outcome = %v", outcome)
	}

	later := now.Add(40 * time.Second)
	cs := freshContext(62, later)
	cs.HasPrep = true
	cs.PrepWrittenAt = now.Add(35 * time.Second)

	outcome := compactTick(t, cfg, client, pane, ps, cs, later)

	if outcome != OutcomeCompactSent {
		t.Fatalf("2 段目: outcome = %v, want %v", outcome, OutcomeCompactSent)
	}
	assertSentText(t, client, cfg.CompactAutoMessage)
	if !ps.CompactPrepSentAt.IsZero() {
		t.Errorf("CompactPrepSentAt はクリアされるべき: %v", ps.CompactPrepSentAt)
	}
}

func TestTick_stateFileが投入より古いままなら待つ(t *testing.T) {
	// 前のサイクルの state file が残っているだけで /compact を撃たない｡
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{CompactPrepSentAt: now}
	cs := freshContext(62, now)
	cs.HasPrep = true
	cs.PrepWrittenAt = now.Add(-time.Hour)

	outcome := compactTick(t, compactAutoConfig(), client, pane, ps, cs, now.Add(30*time.Second))

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_stateFileが現れないまま時間切れになったら諦める(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	cfg := compactAutoConfig()
	ps := &PaneState{CompactPrepSentAt: now}
	timedOut := now.Add(time.Duration(cfg.CompactAutoPrepTimeoutMinutes)*time.Minute + time.Second)

	outcome := compactTick(t, cfg, client, pane, ps, freshContext(62, timedOut), timedOut)

	if outcome != OutcomeCompactPrepTimeout {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactPrepTimeout)
	}
	assertNoSend(t, client)

	// 諦めた直後はクールダウン中なので､閾値を超えていても再投入しない｡
	next := timedOut.Add(time.Minute)
	if outcome := compactTick(t, cfg, client, pane, ps, freshContext(62, next), next); outcome != OutcomeMonitoring {
		t.Errorf("クールダウン中: outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_圧縮完了markerを見て再開を促す(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}
	cfg := compactAutoConfig()
	// 圧縮直後は使用率が落ちるので 1 段目は動かない｡
	cs := freshContext(9, now)
	cs.HasCompacted = true
	cs.CompactedAt = now.Add(-time.Duration(cfg.CompactResumeDelaySeconds+1) * time.Second)

	outcome := compactTick(t, cfg, client, pane, ps, cs, now)

	if outcome != OutcomeCompactResumed {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactResumed)
	}
	assertSentText(t, client, cfg.CompactStallMessage)

	// 同じ marker で二度送らない (復旧 hook が marker を消し損ねても突き続けない)｡
	next := now.Add(5 * time.Minute)
	client.Calls = nil
	if outcome := compactTick(t, cfg, client, pane, ps, cs, next); outcome != OutcomeMonitoring {
		t.Errorf("2 度目: outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_圧縮直後は猶予を置いてから再開を促す(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	cfg := compactAutoConfig()
	cs := freshContext(9, now)
	cs.HasCompacted = true
	cs.CompactedAt = now.Add(-time.Duration(cfg.CompactResumeDelaySeconds-10) * time.Second)

	outcome := compactTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), &PaneState{}, cs, now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_compactResumeEnabledがfalseなら再開を促さない(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	cfg := compactAutoConfig()
	cfg.CompactResumeEnabled = false
	cs := freshContext(9, now)
	cs.HasCompacted = true
	cs.CompactedAt = now.Add(-time.Hour)

	outcome := compactTick(t, cfg, client, newTestPane(herdrcli.AgentStatusIdle, true), &PaneState{}, cs, now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_compactStateが無ければ何もしない(t *testing.T) {
	// Deps.CompactState を差していない daemon (あるいは session ID を持たない pane)｡
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}

	outcome := compactTick(t, compactAutoConfig(), client, newTestPane(herdrcli.AgentStatusIdle, true), &PaneState{}, nil, now)

	if outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, client)
}

// assertSentText は pane へ text が送られ､Enter で投入されたことを確かめる｡
func assertSentText(t *testing.T, client *herdrclifake.Client, text string) {
	t.Helper()
	if client.SentTexts[len(client.SentTexts)-1] != text {
		t.Errorf("送ったテキスト = %q, want %q", client.SentTexts, text)
	}
	var sawEnter bool
	for _, call := range client.Calls {
		if call == "SendKeys:pane-1" {
			sawEnter = true
		}
	}
	if !sawEnter {
		t.Error("Enter が送られていない")
	}
}
