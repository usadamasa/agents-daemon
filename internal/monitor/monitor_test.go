package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// 画面テキストのテストフィクスチャ。
// detect パッケージの二重シグナル要件 (limit パターン + 近傍の reset パターン) を
// 満たすものだけを使う。単発の "limit" という単語だけでは KindLimit にならない。
const (
	// limitAndRelativeScreen は 1 行で limit + 相対時刻表現の両方を満たす。
	// classify では KindLimit、ParseRelativeWait では 5 分と解釈される。
	limitAndRelativeScreen = "Try again in 5 minutes"
	// limitAbsoluteScreen は limit フレーズと、相対表現でも am/pm の絶対時刻でもない
	// reset 表現の組み合わせ。ParseRelativeWait も ParseAbsoluteReset も解釈できない
	// (fallback 経路のテストに使う)。
	limitAbsoluteScreen = "Claude usage limit reached.\nYour limit resets at 15:00."
	// limitAmPmScreen は実機と同じ形の絶対時刻 (am/pm + tz) を含む。
	// ParseAbsoluteReset が読める (相対表現は無い)。
	limitAmPmScreen = "⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)"
	// transientScreen は直近出力ブロック内の 5xx エラー文言。
	transientScreen = "⏺ API Error: 500 Internal Server Error"
	// cleanScreen は limit にも transient にも該当しない通常の画面。
	cleanScreen = "$ some normal claude output\n⏺ done"
)

func newTestClassifier(t *testing.T) *detect.Classifier {
	t.Helper()
	c, err := detect.NewClassifier(nil, nil)
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}
	return c
}

func newTestPane(status herdrcli.AgentStatus, isClaude bool) herdrcli.Pane {
	agent := "bash"
	if isClaude {
		agent = "claude-code"
	}
	return herdrcli.Pane{
		Agent:       agent,
		AgentStatus: status,
		PaneID:      "pane-1",
	}
}

func newTestDeps(t *testing.T, cfg config.Config, client *herdrclifake.Client) Deps {
	t.Helper()
	return Deps{
		Client:     client,
		Classifier: newTestClassifier(t),
		Config:     cfg,
		Sleep:      func(time.Duration) {},
	}
}

func readReturning(screen string, err error) func(ctx context.Context, paneID string, opts herdrcli.PaneReadOptions) (string, error) {
	return func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
		return screen, err
	}
}

func TestTick_AgentStatusNeverGatesDetection(t *testing.T) {
	// herdr の agent_status は PTY の出力活動から推定される値で、上限で止まった
	// pane を working と報告し続けることがある (teammate ウィジェットの経過時間
	// カウンタのように毎秒再描画される要素があると idle へ落ちない)。
	//
	// 上限で止まった pane は「working に見える pane」と区別できないため、
	// agent_status を判定に使うと上限を取りこぼす。判定は画面テキストの分類
	// (detect.Classifier) だけで行う。
	for _, status := range []herdrcli.AgentStatus{
		herdrcli.AgentStatusWorking,
		herdrcli.AgentStatusIdle,
		herdrcli.AgentStatusBlocked,
		herdrcli.AgentStatusDone,
		herdrcli.AgentStatusUnknown,
	} {
		t.Run(string(status), func(t *testing.T) {
			client := &herdrclifake.Client{
				PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
			}
			deps := newTestDeps(t, config.Default(), client)
			pane := newTestPane(status, true)
			ps := &PaneState{}
			now := time.Now()

			outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != OutcomeWaiting {
				t.Errorf("outcome = %v, want %v (agent_status によらず上限を検知すべき)", outcome, OutcomeWaiting)
			}
			if ps.Status != StatusWaiting {
				t.Errorf("ps.Status = %v, want StatusWaiting", ps.Status)
			}
		})
	}
}

func TestTick_StillWaiting_NoRead(t *testing.T) {
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	now := time.Now()
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(time.Hour)}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeStillWaiting {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeStillWaiting)
	}
	if len(client.Calls) != 0 {
		t.Errorf("Calls = %v, want 空 (起床時刻前は画面すら読まない)", client.Calls)
	}
}

func TestTick_LimitDetected_ResetsAtWinsOverFallback(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(2 * time.Hour)
	rateState := &sessionstate.RateLimit{
		FiveHourUsed:     95,
		FiveHourResetsAt: resetsAt,
		ObservedAt:       now.Add(-time.Minute),
	}

	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	want := resetsAt.Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (画面の相対表現やfallbackではなくresets_atが優先されるべき)", ps.WaitUntil, want)
	}
	if ps.Status != StatusWaiting {
		t.Errorf("Status = %v, want %v", ps.Status, StatusWaiting)
	}
}

func TestTick_StaleState_StillUsesResetsAt(t *testing.T) {
	// 上限で止まったセッションは statusline を再描画しないため、state は必ず古くなる。
	// resets_at は絶対時刻なので古くても正しい。ここで捨てると、正確な解除時刻を
	// 持っているのに画面スクレイプへ後退してしまう。
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	rateState := &sessionstate.RateLimit{
		FiveHourUsed:     95,
		FiveHourResetsAt: now.Add(2 * time.Hour),
		ObservedAt:       now.Add(-2 * time.Hour), // StateMaxAgeSeconds (既定 900s) を超え陳腐化
	}

	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil), // 「5分後」を含む
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	want := now.Add(2 * time.Hour).Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (state が古くても resets_at は使うべき)", ps.WaitUntil, want)
	}
}

func TestTick_NoStateNoRelative_FallsBackToFallbackHours(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)

	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAbsoluteScreen, nil), // 相対表現を含まない
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	fallback := time.Duration(cfg.FallbackWaitHours * float64(time.Hour))
	want := now.Add(fallback).Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (state も相対表現も無ければ FallbackWaitHours)", ps.WaitUntil, want)
	}
}

func TestTick_Gate_FreshStateThreshold(t *testing.T) {
	// ゲートは「fresh な state があり、かつ使用率が閾値未満」の場合だけ発火を見送る。
	// state が陳腐化していれば使用率に関わらず判断材料が無いとみなし、画面だけで判定する。
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	cfg := config.Default() // UsedPercentageThreshold = 90, StateMaxAgeSeconds = 900

	tests := []struct {
		name        string
		fresh       bool
		usedPercent float64
		want        Outcome
	}{
		{"fresh_閾値未満_見送り", true, 50, OutcomeGateSuppressed},
		{"fresh_閾値以上_発火", true, 95, OutcomeWaiting},
		{"stale_閾値未満_見送らず発火", false, 50, OutcomeWaiting},
		{"stale_閾値以上_発火", false, 95, OutcomeWaiting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedAt := now.Add(-time.Minute)
			if !tt.fresh {
				observedAt = now.Add(-2 * time.Hour)
			}
			rateState := &sessionstate.RateLimit{
				FiveHourUsed:     tt.usedPercent,
				FiveHourResetsAt: now.Add(2 * time.Hour),
				ObservedAt:       observedAt,
			}
			client := &herdrclifake.Client{
				PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
			}
			deps := newTestDeps(t, cfg, client)
			pane := newTestPane(herdrcli.AgentStatusIdle, true)
			ps := &PaneState{}

			outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != tt.want {
				t.Errorf("outcome = %v, want %v", outcome, tt.want)
			}
			wantStatus := StatusMonitoring
			if tt.want == OutcomeWaiting {
				wantStatus = StatusWaiting
			}
			if ps.Status != wantStatus {
				t.Errorf("Status = %v, want %v", ps.Status, wantStatus)
			}
		})
	}
}

// TestTick_Gate_NullFiveHour_DoesNotSuppress は five_hour が null で書かれた state
// (5 時間ウィンドウの切り替わり瞬間の statusline 描画) を「fresh な 0%」と読んで
// ゲートで抑制しないことを確かめる。判断材料が無いのだから、state 無しと同じ扱いで
// 画面判定をそのまま通す。
func TestTick_Gate_NullFiveHour_DoesNotSuppress(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	rateState := &sessionstate.RateLimit{
		// five_hour が null で書かれた state。FiveHourResetsAt がゼロ値のままになる。
		ObservedAt: now.Add(-time.Minute), // fresh
	}
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Errorf("outcome = %v, want %v (five_hour が null なら使用率 0%% として抑制してはいけない)", outcome, OutcomeWaiting)
	}
}

// TestTick_WakeWithLimitScreen_SendsDespiteFreshGate は待機明けに画面がまだ上限表示の
// とき、fresh な state の使用率が閾値未満でも送信することを確かめる。
//
// 解除時刻を過ぎると 5 時間ウィンドウは切り替わり使用率は下がるが、Claude Code は
// 上限の画面のままキー入力を待つ。ここでゲートを効かせると「ユーザー自身が復帰した」と
// 取り違えて監視へ戻り、15 分後に state が古くなってから改めて検知して、次のウィンドウの
// resets_at (5 時間先) まで寝てしまう (2026-08-28 03:11 の取りこぼし)。
// ゲートは待機に入るときの誤検知よけであって、待機明けの送信を止めるものではない。
func TestTick_WakeWithLimitScreen_SendsDespiteFreshGate(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	rateState := &sessionstate.RateLimit{
		FiveHourUsed:     5,
		FiveHourResetsAt: now.Add(5 * time.Hour),
		ObservedAt:       now.Add(-time.Minute), // fresh かつ閾値未満
	}
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAbsoluteScreen, nil),
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second)}

	outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Fatalf("outcome = %v, want %v (待機明けの上限画面には送信すべき)", outcome, OutcomeRetried)
	}
	if ps.Status != StatusWaiting {
		t.Errorf("Status = %v, want %v (送信後は結果確認のため待機を継続)", ps.Status, StatusWaiting)
	}
	if ps.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", ps.Attempts)
	}
	sent := false
	for _, call := range client.Calls {
		if call == "SendText:pane-1" {
			sent = true
		}
	}
	if !sent {
		t.Errorf("Calls = %v, SendText が呼ばれていない", client.Calls)
	}
}

func TestTick_UserResumedDuringWait(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(cleanScreen, nil),
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{Status: StatusWaiting, Attempts: 3, WaitUntil: now.Add(-time.Second)}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRecoveredByUser {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeRecoveredByUser)
	}
	if ps.Status != StatusMonitoring {
		t.Errorf("Status = %v, want %v", ps.Status, StatusMonitoring)
	}
	if ps.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 (ユーザー自身の resume では nudge しない)", ps.Attempts)
	}
	for _, call := range client.Calls {
		if call == "SendText:pane-1" || call == "SendKeys:pane-1" {
			t.Errorf("Calls = %v, ユーザーが自力で resume したときは何も送ってはいけない", client.Calls)
		}
	}
}

func TestTick_TransientBackoff_DoublesAndCaps(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(transientScreen, nil),
	}
	cfg := config.Default() // TransientWaitSeconds=60, TransientMaxWaitSeconds=300
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{Status: StatusWaiting} // WaitUntil ゼロ値なので初回から必ず進む

	wantDeltas := []time.Duration{
		120 * time.Second, // 60 * 2^1
		240 * time.Second, // 60 * 2^2
		300 * time.Second, // 60 * 2^3 = 480 は cap の 300 に切り詰め
	}

	tickNow := now
	for i, wantDelta := range wantDeltas {
		outcome, err := Tick(context.Background(), deps, pane, ps, nil, tickNow)
		if err != nil {
			t.Fatalf("Tick() [%d] error = %v", i, err)
		}
		if outcome != OutcomeRetried {
			t.Fatalf("outcome[%d] = %v, want %v", i, outcome, OutcomeRetried)
		}
		gotDelta := ps.WaitUntil.Sub(tickNow)
		if gotDelta != wantDelta {
			t.Errorf("attempt %d: delta = %v, want %v", i+1, gotDelta, wantDelta)
		}
		tickNow = ps.WaitUntil // 次の tick はちょうど起床時刻に進める
	}
	if ps.Attempts != len(wantDeltas) {
		t.Errorf("Attempts = %d, want %d", ps.Attempts, len(wantDeltas))
	}
}

func TestTick_MaxRetriesReached_NoSend(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil), // KindLimit (transient ではない)
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{Status: StatusWaiting, Attempts: cfg.MaxRetries}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeMaxRetries {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeMaxRetries)
	}
	want := now.Add(time.Duration(cfg.PollIntervalSeconds*maxRetriesCooldownMultiplier) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
	}
	for _, call := range client.Calls {
		if call == "SendText:pane-1" || call == "SendKeys:pane-1" {
			t.Errorf("Calls = %v, MaxRetries 到達後は送信してはいけない", client.Calls)
		}
	}
}

func TestTick_Recovery_SendsEscTextEnterInOrder(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	const wantMessage = "作業を再開してください"

	var gotText string
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
		SendTextFunc: func(_ context.Context, _ string, text string) error {
			gotText = text
			return nil
		},
	}
	cfg := config.Default()
	cfg.RetryMessage = wantMessage
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeRetried)
	}

	wantCalls := []string{"PaneRead:pane-1", "SendKeys:pane-1", "SendText:pane-1", "SendKeys:pane-1"}
	if len(client.Calls) != len(wantCalls) {
		t.Fatalf("Calls = %v, want %v", client.Calls, wantCalls)
	}
	for i, want := range wantCalls {
		if client.Calls[i] != want {
			t.Errorf("Calls[%d] = %q, want %q (esc → 送信 → enter の順を厳守すべき)", i, client.Calls[i], want)
		}
	}
	if gotText != wantMessage {
		t.Errorf("送信テキスト = %q, want %q (RetryMessage を改変せずそのまま送るべき)", gotText, wantMessage)
	}
}

func TestTick_NotClaudeAgent_DuringWait_NoSend(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, false) // Claude agent ではなくなった
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeNotClaudeAgent {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeNotClaudeAgent)
	}
	want := now.Add(time.Duration(cfg.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
	}
	if len(client.Calls) != 1 || client.Calls[0] != "PaneRead:pane-1" {
		t.Errorf("Calls = %v, want [PaneRead:pane-1] のみ (Claude agent でなければ送信しない)", client.Calls)
	}
}

func TestTick_ScreenUnreadable_DuringWait_StaysWaiting(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning("", nil), // 空画面
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{Status: StatusWaiting, Attempts: 3}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeScreenUnreadable {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeScreenUnreadable)
	}
	if ps.Status != StatusWaiting || ps.Attempts != 3 {
		t.Errorf("ps = %+v, 空画面は resume の証拠にならないので状態を変えてはいけない", ps)
	}
}

func TestTick_ReadError_DoesNotKillMachine(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	boom := errors.New("herdr unreachable")

	t.Run("監視中", func(t *testing.T) {
		client := &herdrclifake.Client{PaneReadFunc: readReturning("", boom)}
		deps := newTestDeps(t, config.Default(), client)
		pane := newTestPane(herdrcli.AgentStatusIdle, true)
		ps := &PaneState{}

		outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

		if err == nil {
			t.Fatal("err = nil, want non-nil (呼び出し元が連続失敗をカウントできるようにエラーを返すべき)")
		}
		if outcome != OutcomeReadError {
			t.Errorf("outcome = %v, want %v", outcome, OutcomeReadError)
		}
		if ps.Status != StatusMonitoring {
			t.Errorf("Status = %v, want %v (エラーで状態を破壊してはいけない)", ps.Status, StatusMonitoring)
		}
	})

	t.Run("待機中", func(t *testing.T) {
		client := &herdrclifake.Client{PaneReadFunc: readReturning("", boom)}
		deps := newTestDeps(t, config.Default(), client)
		pane := newTestPane(herdrcli.AgentStatusIdle, true)
		ps := &PaneState{Status: StatusWaiting, Attempts: 2}

		outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

		if err == nil {
			t.Fatal("err = nil, want non-nil")
		}
		if outcome != OutcomeScreenUnreadable {
			t.Errorf("outcome = %v, want %v", outcome, OutcomeScreenUnreadable)
		}
		if ps.Status != StatusWaiting || ps.Attempts != 2 {
			t.Errorf("ps = %+v, 読み取りエラーで待機状態を崩してはいけない", ps)
		}
	})
}

func TestTick_TransientFromMonitoring_NoMarginAdded(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(transientScreen, nil),
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	want := now.Add(time.Duration(cfg.TransientWaitSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (transient の初回待機に margin は乗せない)", ps.WaitUntil, want)
	}

	var gotLabel bool
	for _, call := range client.Calls {
		if call == "ReportMetadata:pane-1" {
			gotLabel = true
		}
	}
	if !gotLabel {
		t.Errorf("Calls = %v, 待機に入ったら engaged ラベルを表示するべき", client.Calls)
	}
}

func TestTick_HandleTransientDisabled_StaysMonitoring(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(transientScreen, nil),
	}
	cfg := config.Default()
	cfg.HandleTransient = false
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeMonitoring {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	if ps.Status != StatusMonitoring {
		t.Errorf("Status = %v, want %v", ps.Status, StatusMonitoring)
	}
	if len(client.Calls) != 1 || client.Calls[0] != "PaneRead:pane-1" {
		t.Errorf("Calls = %v, want [PaneRead:pane-1] のみ", client.Calls)
	}
}

func TestTick_CleanScreen_StaysMonitoring(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(cleanScreen, nil),
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeMonitoring {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	if ps.Status != StatusMonitoring {
		t.Errorf("Status = %v, want %v", ps.Status, StatusMonitoring)
	}
}

// detect.Kind を直接参照して分類フィクスチャの前提が崩れていないことを確認する。
// フィクスチャの文言を変えたときに、意図した Kind から静かにズレるのを防ぐ。
func TestFixtures_ClassifyAsExpected(t *testing.T) {
	c := newTestClassifier(t)
	tests := []struct {
		name   string
		screen string
		want   detect.Kind
	}{
		{"limitAndRelativeScreen", limitAndRelativeScreen, detect.KindLimit},
		{"limitAbsoluteScreen", limitAbsoluteScreen, detect.KindLimit},
		{"transientScreen", transientScreen, detect.KindTransient},
		{"cleanScreen", cleanScreen, detect.KindNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Classify(tt.screen, 0); got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.screen, got, tt.want)
			}
		})
	}
}

// TestTick_PastResetsAt_WakesImmediately は resets_at を過ぎているのに画面が上限
// 表示のまま止まっている pane を、待たずに起こすことを確かめる。
//
// 解除時刻を過ぎても Claude Code はキー入力を待って止まったままになる。ここで
// fallbackWaitHours へ落ちると、待つ理由が無いのに数時間寝てしまう
// (2026-08-19 の pane が実際にこの状態だった)。
func TestTick_PastResetsAt_WakesImmediately(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	rateState := &sessionstate.RateLimit{
		FiveHourUsed:     100,
		FiveHourResetsAt: now.Add(-time.Hour),
		ObservedAt:       now.Add(-3 * time.Hour), // 上限で描画が止まったので state も古い
	}

	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil), // 「5分後」を含む
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	want := now.Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (解除済みなら画面の相対表現より優先して即起床すべき)", ps.WaitUntil, want)
	}
}

// TestTick_StateWithoutResetsAt_FallsBackToRelativeParse は resets_at を持たない
// state (7 日ウィンドウしか入っていない等) では画面の相対表現へ落ちることを確かめる。
// epoch 0 を「過去の解除時刻」と読み違えて即起床させないための境界。
func TestTick_StateWithoutResetsAt_FallsBackToRelativeParse(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	rateState := &sessionstate.RateLimit{
		FiveHourUsed: 100,
		ObservedAt:   now,
	}

	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil), // 「5分後」を含む
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, rateState, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	want := now.Add(5 * time.Minute).Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (resets_at が無いなら相対表現へ落ちるべき)", ps.WaitUntil, want)
	}
}
