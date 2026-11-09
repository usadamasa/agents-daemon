package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// transcript の末尾のフィクスチャ。本文は実機で踏んだ上限の entry から写した (transcript/testdata)。
const (
	uuidLimit  = "cae11b0e-b253-403c-8cd6-7cf350809a97"
	uuidLimit2 = "0f3c9a7e-2b4d-4c6e-8a1b-3d5f7e9a1c2b"
)

// limitedTurn は 5 時間ウィンドウの上限で止まった transcript の末尾。entry の時刻は解除時刻の 5 時間前 (窓の頭で踏んだ形)。
func limitedTurn(uuid string, resetsAt time.Time) Turn {
	return Turn{Path: "/home/x/.claude/projects/-work/sid.jsonl", Entry: &transcript.Entry{
		UUID: uuid, Type: "assistant", Error: "rate_limit", IsAPIError: true, APIErrorStatus: 429, Version: "2.1.289",
		Timestamp: resetsAt.Add(-5 * time.Hour),
		Text:      "You've hit your session limit · resets 7:50pm (Asia/Tokyo)",
		Quota:     &transcript.Quota{Status: "rejected", RateLimitType: "five_hour", ResetsAt: resetsAt, OverageStatus: "rejected"},
	}}
}

// datedTurn は turn の entry の時刻を at にしたもの。
func datedTurn(turn Turn, at time.Time) Turn {
	e := *turn.Entry
	e.Timestamp = at
	return Turn{Path: turn.Path, Entry: &e}
}

// legacyLimitedTurn は quotaLimits の無い古い形の上限の entry (2.1.233 以前)。解除時刻が無いので上限として追わない。
func legacyLimitedTurn(now time.Time) Turn {
	return Turn{Path: "/t.jsonl", Entry: &transcript.Entry{
		UUID: "legacy", Type: "assistant", Error: "rate_limit", IsAPIError: true, Version: "2.1.224", Timestamp: now,
		Text: "You've hit your session limit · resets 5:50pm (Asia/Tokyo)",
	}}
}

func weeklyTurn(resetsAt time.Time) Turn {
	return Turn{Path: "/t.jsonl", Entry: &transcript.Entry{
		UUID: "weekly", Type: "assistant", Error: "rate_limit", IsAPIError: true, APIErrorStatus: 429, Timestamp: resetsAt.Add(-time.Hour),
		Text:  "You've hit your weekly limit · resets Aug 25 at 1pm (Asia/Tokyo) · progress saved",
		Quota: &transcript.Quota{Status: "rejected", RateLimitType: "seven_day", ResetsAt: resetsAt},
	}}
}

// transientTurn は server_error で終わった turn (時刻は at)。
func transientTurn(at time.Time) Turn {
	return Turn{Path: "/t.jsonl", Entry: &transcript.Entry{
		UUID: "transient", Type: "assistant", Error: "server_error", IsAPIError: true, Timestamp: at,
		Text: "API Error: Your computer went to sleep mid-response. The response above may be incomplete.",
	}}
}

// hardCapTurn は Fable のモデル単位の上限 (quotaLimits 無し)。
func hardCapTurn() Turn {
	return Turn{Path: "/t.jsonl", Entry: &transcript.Entry{
		UUID: "fable", Type: "assistant", Error: "rate_limit", IsAPIError: true, APIErrorStatus: 429,
		Text: "You've reached your Fable 5 limit. Run /usage-credits to continue or switch models with /model.",
	}}
}

// resumedTurn は利用者 (か daemon) の次の prompt が最後の turn。
func resumedTurn() Turn {
	return Turn{Path: "/t.jsonl", Entry: &transcript.Entry{UUID: "user-1", Type: "user", Text: "続けて"}}
}

// noSessionTurn と unreadableTurn は transcript が読めなかった結果。
var (
	noSessionTurn  = Turn{Reason: "pane に session_id が無い"}
	unreadableTurn = Turn{Reason: "transcript が見つからない"}
)

// 画面のフィクスチャ。上限の判定には使わず、待機明けのキー選択だけに効く。
const (
	// cleanScreen は通知欄に何も無い画面 (上限の文は transcript の 1 行として残っている)。
	cleanScreen = "⏺ You've hit your session limit · resets 7:50pm (Asia/Tokyo)\n\n❯ \n"
	// nativeArmedScreen は Claude Code 自身の auto-continue がカウントダウン中の画面。
	nativeArmedScreen = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)\n\n" +
		"Usage limit reached · continuing automatically at 5:00pm · esc or type to cancel"
	// nativeRetryingScreen は unattended retry mode が request の中で待っている画面。
	nativeRetryingScreen = "⚠ Session limit reached · Retrying in 3h 12m (5pm) · attempt 4/10"
	// resumeReadyScreen は解除済みで Enter だけを待っている画面。
	resumeReadyScreen = "  ⚠ Your usage limit has reset\n      Press enter to continue"
	// lowerPriorityScreen は /low-priority で作業を続けている画面。
	lowerPriorityScreen = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)\n\n" +
		"Continuing now at lower priority until your limit resets at 5pm. Your weekly limit still applies. Run /low-priority to stop."
)

func newTestPane(status multiplexer.AgentStatus, isClaude bool) multiplexer.Pane {
	agent := "bash"
	if isClaude {
		agent = "claude-code"
	}
	return multiplexer.Pane{Agent: agent, AgentStatus: status, PaneID: "pane-1", SessionID: "sid-1"}
}

// turnsReturning は session_id が空なら noSessionTurn、それ以外は turn を返す Turns。
func turnsReturning(turn Turn) func(string) Turn {
	return func(sessionID string) Turn {
		if sessionID == "" {
			return noSessionTurn
		}
		return turn
	}
}

func newTestDeps(_ *testing.T, cfg config.Config, client *multiplexerfake.Client, turn Turn) Deps {
	return Deps{
		Client: client,
		Config: cfg,
		Sleep:  func(time.Duration) {},
		Turns:  turnsReturning(turn),
	}
}

func readReturning(screen string, err error) func(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	return func(context.Context, string, multiplexer.PaneReadOptions) (string, error) {
		return screen, err
	}
}

func assertNoSend(t *testing.T, client *multiplexerfake.Client) {
	t.Helper()
	for _, call := range client.Calls {
		if call == "SendText:pane-1" || call == "SendKeys:pane-1" {
			t.Errorf("送信が発生した: %v", client.Calls)
			return
		}
	}
}

func assertNoRead(t *testing.T, client *multiplexerfake.Client) {
	t.Helper()
	if len(client.Calls) != 0 {
		t.Errorf("Calls = %v, want 空 (画面を読むのは待機明けだけ)", client.Calls)
	}
}

// record が working の pane には、transcript が何と言っていても触らない。上限なのに working なら食い違いを記録する。
func TestTick_WorkingPaneIsNeverTouched(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		turn Turn
		want Outcome
	}{
		{"普通の turn", resumedTurn(), OutcomeMonitoring},
		{"上限の entry (書き込みの遅れか前提の崩れ)", limitedTurn(uuidLimit, now.Add(time.Hour)), OutcomeWorkingSuppressed},
		{"一時エラーの entry", transientTurn(now), OutcomeWorkingSuppressed},
		{"hard cap の entry", hardCapTurn(), OutcomeWorkingSuppressed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
			deps := newTestDeps(t, config.Default(), client, tt.turn)
			ps := &PaneState{}

			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusWorking, true), ps, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != tt.want || ps.Status != StatusMonitoring {
				t.Errorf("outcome = %v status = %v, want %v / monitoring", outcome, ps.Status, tt.want)
			}
			assertNoRead(t, client)
		})
	}
}

// 待機中に record が working になったら、誰かが再開したので監視へ戻る。
func TestTick_WorkingDuringWait_IsRecovered(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{}
	deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(-time.Hour)))
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 2, TurnUUID: uuidLimit}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusWorking, true), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRecoveredByUser || *ps != (PaneState{}) {
		t.Errorf("outcome = %v ps = %+v, want recovered-by-user / ゼロ値", outcome, ps)
	}
	assertNoRead(t, client)
}

// session_id が引けない、transcript が読めない pane は検知の対象外。待機中なら読めないことを再開の証拠にせず延長する。
func TestTick_UnavailableTurn(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cfg := config.Default()
	tests := []struct {
		name      string
		sessionID string
		turn      Turn
		want      Outcome
	}{
		{"session_id が無い", "", noSessionTurn, OutcomeNoSession},
		{"transcript が読めない", "sid-1", unreadableTurn, OutcomeTranscriptUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/監視中", func(t *testing.T) {
			client := &multiplexerfake.Client{}
			deps := newTestDeps(t, cfg, client, tt.turn)
			pane := newTestPane(multiplexer.AgentStatusIdle, true)
			pane.SessionID = tt.sessionID
			ps := &PaneState{}

			outcome, err := Tick(context.Background(), deps, pane, ps, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != tt.want || *ps != (PaneState{}) {
				t.Errorf("outcome = %v ps = %+v, want %v / 監視のまま", outcome, ps, tt.want)
			}
			assertNoRead(t, client)
		})
		t.Run(tt.name+"/待機中", func(t *testing.T) {
			client := &multiplexerfake.Client{}
			deps := newTestDeps(t, cfg, client, tt.turn)
			pane := newTestPane(multiplexer.AgentStatusIdle, true)
			pane.SessionID = tt.sessionID
			ps := &PaneState{Status: StatusWaiting, Attempts: 2, TurnUUID: uuidLimit}

			outcome, err := Tick(context.Background(), deps, pane, ps, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != tt.want || ps.Status != StatusWaiting || ps.Attempts != 2 {
				t.Errorf("outcome = %v ps = %+v, want %v / 待機を続ける", outcome, ps, tt.want)
			}
			if want := now.Add(time.Duration(cfg.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second); !ps.WaitUntil.Equal(want) {
				t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
			}
			assertNoRead(t, client)
		})
	}
}

func TestTick_StillWaiting_ReadsNothing(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{}
	deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(time.Hour)))
	turns := 0
	deps.Turns = func(string) Turn { turns++; return limitedTurn(uuidLimit, now.Add(time.Hour)) }
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(time.Hour)}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), ps, now)

	if err != nil || outcome != OutcomeStillWaiting {
		t.Fatalf("Tick() = %v, %v, want still-waiting", outcome, err)
	}
	if turns != 0 || len(client.Calls) != 0 {
		t.Errorf("Turns %d 回、Calls %v: 起床時刻前は transcript も画面も読まない", turns, client.Calls)
	}
}

// 上限の検知は transcript の entry の resetsAt で待つ。画面は読まない。
func TestTick_LimitDetected_WaitsForResetsAt(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(2 * time.Hour)
	cfg := config.Default()
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, resetsAt))
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting || ps.Status != StatusWaiting {
		t.Fatalf("outcome = %v status = %v, want waiting", outcome, ps.Status)
	}
	if want := resetsAt.Add(time.Duration(cfg.MarginSeconds) * time.Second); !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (resetsAt + margin)", ps.WaitUntil, want)
	}
	if ps.TurnUUID != uuidLimit {
		t.Errorf("TurnUUID = %q, want %q", ps.TurnUUID, uuidLimit)
	}
	assertNoRead(t, client)
}

// 解除時刻を過ぎても Claude Code はキー入力を待って止まったままになる。待つ理由は無いので margin 後に起こす。
func TestTick_PastResetsAt_WakesImmediately(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cfg := config.Default()
	client := &multiplexerfake.Client{}
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Hour)))
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), ps, now)

	if err != nil || outcome != OutcomeWaiting {
		t.Fatalf("Tick() = %v, %v, want waiting", outcome, err)
	}
	if want := now.Add(time.Duration(cfg.MarginSeconds) * time.Second); !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (now + margin)", ps.WaitUntil, want)
	}
}

// quotaLimits の無い古い形の上限の entry (2.1.233 以前) は解除時刻が無いので追わない。待つ理由が決められないまま
// フォールバックで寝るより、見逃して StopFailure の記録との食い違いのログで気づく側に倒す。
func TestTick_LegacyEntry_IsNotLimit(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{}
	deps := newTestDeps(t, config.Default(), client, legacyLimitedTurn(now))
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

	if err != nil || outcome != OutcomeMonitoring || ps.Status != StatusMonitoring {
		t.Errorf("Tick() = %v, %v, ps = %+v; want monitoring", outcome, err, ps)
	}
	assertNoRead(t, client)
}

// 待機明けに transcript がまだ同じ上限なら、画面を 1 回読み、通知欄が無ければ esc → 文面 → enter の順に送る。
func TestTick_Wake_SendsEscTextEnterInOrder(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	const wantMessage = "作業を再開してください"
	var gotText string
	client := &multiplexerfake.Client{
		PaneReadFunc: readReturning(cleanScreen, nil),
		SendTextFunc: func(_ context.Context, _ string, text string) error { gotText = text; return nil },
	}
	cfg := config.Default()
	cfg.RetryMessage = wantMessage
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-2*time.Minute)))
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), TurnUUID: uuidLimit}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

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
	if ps.Status != StatusWaiting || ps.Attempts != 1 || !ps.WaitUntil.Equal(now.Add(retryWaitAfterSend)) {
		t.Errorf("ps = %+v, want 待機を続け attempts 1、%s 後に確認", ps, retryWaitAfterSend)
	}
}

// 待機明けに transcript の末尾が利用者の prompt になっていれば、利用者が再開した。何も送らず監視へ戻る。
func TestTick_Wake_UserResumed(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
	deps := newTestDeps(t, config.Default(), client, resumedTurn())
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 3, TurnUUID: uuidLimit}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRecoveredByUser || *ps != (PaneState{}) {
		t.Errorf("outcome = %v ps = %+v, want recovered-by-user / ゼロ値 (ユーザー自身の resume では nudge しない)", outcome, ps)
	}
	assertNoRead(t, client)
}

// 待っている間に新しい依頼 (peer の SendMessage など) がまた上限に当たると、別の uuid の entry になる。
// 解除時刻が先なら送らず待ち直し、試行回数は持ち越す。過ぎていればそのまま送る。
func TestTick_Wake_NewErrorEntry(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cfg := config.Default()
	margin := time.Duration(cfg.MarginSeconds) * time.Second

	t.Run("解除時刻が未来なら待ち直す", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit2, now.Add(4*time.Hour)))
		ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 2, TurnUUID: uuidLimit}

		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

		if err != nil || outcome != OutcomeWaiting {
			t.Fatalf("Tick() = %v, %v, want waiting", outcome, err)
		}
		if ps.TurnUUID != uuidLimit2 || ps.Attempts != 2 || !ps.WaitUntil.Equal(now.Add(4*time.Hour).Add(margin)) {
			t.Errorf("ps = %+v, want 新しい uuid、attempts 2 のまま、新しい解除時刻 + margin", ps)
		}
		assertNoRead(t, client)
	})
	t.Run("解除時刻が過去ならそのまま送る", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit2, now.Add(-time.Minute)))
		ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 2, TurnUUID: uuidLimit}

		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

		if err != nil || outcome != OutcomeRetried {
			t.Fatalf("Tick() = %v, %v, want retried", outcome, err)
		}
		if ps.TurnUUID != uuidLimit2 || ps.Attempts != 3 {
			t.Errorf("ps = %+v, want 新しい uuid、attempts 3", ps)
		}
	})
}

func TestTick_MaxRetriesReached_NoSend(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Hour)))
	ps := &PaneState{Status: StatusWaiting, Attempts: cfg.MaxRetries, TurnUUID: uuidLimit}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeMaxRetries {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeMaxRetries)
	}
	if want := now.Add(time.Duration(cfg.PollIntervalSeconds*maxRetriesCooldownMultiplier) * time.Second); !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
	}
	assertNoSend(t, client)
}

func TestTick_NotClaudeAgent_DuringWait_NoSend(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client, limitedTurn(uuidLimit, now.Add(-time.Hour)))
	ps := &PaneState{Status: StatusWaiting, TurnUUID: uuidLimit}

	outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, false), ps, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeNotClaudeAgent {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeNotClaudeAgent)
	}
	if want := now.Add(time.Duration(cfg.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second); !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
	}
	assertNoRead(t, client)
}

// 待機明けに画面を読めない (エラーか空) ときはキーを選べないので送らず、状態も変えない。
func TestTick_Wake_ScreenUnreadable_StaysWaiting(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	boom := errors.New("rex unreachable")
	for name, read := range map[string]func(context.Context, string, multiplexer.PaneReadOptions) (string, error){
		"エラー": readReturning("", boom),
		"空":   readReturning("  \n", nil),
	} {
		t.Run(name, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: read}
			deps := newTestDeps(t, config.Default(), client, limitedTurn(uuidLimit, now.Add(-time.Hour)))
			before := PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 2, TurnUUID: uuidLimit}
			ps := before

			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusIdle, true), &ps, now)

			if outcome != OutcomeReadError {
				t.Errorf("outcome = %v, want %v", outcome, OutcomeReadError)
			}
			if (name == "エラー") != (err != nil) {
				t.Errorf("err = %v", err)
			}
			if ps != before {
				t.Errorf("ps = %+v, 読めないことで状態を変えてはいけない", ps)
			}
			assertNoSend(t, client)
		})
	}
}

func TestTick_Transient(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)

	t.Run("検知時は TransientWaitSeconds だけ待つ (margin 無し)", func(t *testing.T) {
		cfg := config.Default()
		client := &multiplexerfake.Client{}
		deps := newTestDeps(t, cfg, client, transientTurn(now))
		ps := &PaneState{}

		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

		if err != nil || outcome != OutcomeWaiting {
			t.Fatalf("Tick() = %v, %v, want waiting", outcome, err)
		}
		if want := now.Add(time.Duration(cfg.TransientWaitSeconds) * time.Second); !ps.WaitUntil.Equal(want) {
			t.Errorf("WaitUntil = %v, want %v", ps.WaitUntil, want)
		}
		assertNoRead(t, client)
	})

	t.Run("handleTransient が off なら監視のまま", func(t *testing.T) {
		cfg := config.Default()
		cfg.HandleTransient = false
		client := &multiplexerfake.Client{}
		deps := newTestDeps(t, cfg, client, transientTurn(now))
		ps := &PaneState{}

		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

		if err != nil || outcome != OutcomeMonitoring || ps.Status != StatusMonitoring {
			t.Errorf("Tick() = %v, %v, ps = %+v; want monitoring", outcome, err, ps)
		}
	})

	t.Run("待機明けの再送は指数バックオフで、上限で頭打ち", func(t *testing.T) {
		cfg := config.Default() // TransientWaitSeconds=60, TransientMaxWaitSeconds=300
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, cfg, client, transientTurn(now))
		ps := &PaneState{Status: StatusWaiting, TurnUUID: "transient"} // WaitUntil ゼロ値なので初回から必ず進む
		wantDeltas := []time.Duration{120 * time.Second, 240 * time.Second, 300 * time.Second}

		tickNow := now
		for i, wantDelta := range wantDeltas {
			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, tickNow)
			if err != nil || outcome != OutcomeRetried {
				t.Fatalf("Tick()[%d] = %v, %v, want retried", i, outcome, err)
			}
			if got := ps.WaitUntil.Sub(tickNow); got != wantDelta {
				t.Errorf("attempt %d: delta = %v, want %v", i+1, got, wantDelta)
			}
			tickNow = ps.WaitUntil
		}
		if ps.Attempts != len(wantDeltas) {
			t.Errorf("Attempts = %d, want %d", ps.Attempts, len(wantDeltas))
		}
	})
}

// 長く放置された pane (daemon の起動前や `claude --resume` の前に止まったもの) に再開の文面を送らないよう、監視から待機へ
// 入るときは「解除から 24 時間以内」の entry に限る。解除時刻の無い一時エラーは entry の時刻で見る。時刻の読めない entry は
// 送らない側に倒す。
func TestTick_StaleTurnDoesNotWait(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		turn Turn
	}{
		{"上限の解除から 24 時間より経った", datedTurn(limitedTurn(uuidLimit, now.Add(-25*time.Hour)), now.Add(-26*time.Hour))},
		{"週の上限の解除から 24 時間より経った", weeklyTurn(now.Add(-25 * time.Hour))},
		{"一時エラーの entry が 24 時間より古い", transientTurn(now.Add(-25 * time.Hour))},
		{"時刻の無い entry", datedTurn(limitedTurn(uuidLimit, now.Add(time.Hour)), time.Time{})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
			deps := newTestDeps(t, config.Default(), client, tt.turn)
			ps := &PaneState{}

			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)

			if err != nil {
				t.Fatalf("Tick() error = %v", err)
			}
			if outcome != OutcomeStaleTurn || *ps != (PaneState{}) {
				t.Errorf("outcome = %v ps = %+v, want stale-turn / 監視のまま", outcome, ps)
			}
			assertNoRead(t, client)
		})
	}

	t.Run("解除から 24 時間ちょうどまでは待機に入る", func(t *testing.T) {
		deps := newTestDeps(t, config.Default(), &multiplexerfake.Client{}, limitedTurn(uuidLimit, now.Add(-maxTurnAge)))
		ps := &PaneState{}
		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)
		if err != nil || outcome != OutcomeWaiting {
			t.Errorf("Tick() = %v, %v, want waiting", outcome, err)
		}
	})

	t.Run("entry が 24 時間より古くても解除から 24 時間以内なら待機に入る (daemon の再起動で待機を失った pane)", func(t *testing.T) {
		deps := newTestDeps(t, config.Default(), &multiplexerfake.Client{}, datedTurn(limitedTurn(uuidLimit, now.Add(-23*time.Hour)), now.Add(-28*time.Hour)))
		ps := &PaneState{}
		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)
		if err != nil || outcome != OutcomeWaiting {
			t.Errorf("Tick() = %v, %v, want waiting", outcome, err)
		}
	})

	t.Run("待機明けには古さで止めない (待っている間に 24 時間を越える経路のため)", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		turn := datedTurn(limitedTurn(uuidLimit, now.Add(-time.Minute)), now.Add(-30*time.Hour))
		deps := newTestDeps(t, config.Default(), client, turn)
		ps := waitingState(now, 0)
		outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)
		if err != nil || outcome != OutcomeRetried {
			t.Errorf("Tick() = %v, %v, want retried", outcome, err)
		}
	})
}

// 週の上限は解除が 24 時間以内なら 5 時間ウィンドウと同じく待ち、それより先なら待たず記録だけ残す (#338)。
// 解除が 5 日先の entry は、待っている間に 24 時間以内へ入った tick で待機に入る (entry の古さでは止めない)。
func TestTick_WeeklyLimit(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cfg := config.Default()
	margin := time.Duration(cfg.MarginSeconds) * time.Second
	pane := newTestPane(multiplexer.AgentStatusError, true)

	t.Run("監視中", func(t *testing.T) {
		tests := []struct {
			name string
			turn Turn
			want Outcome
		}{
			{"解除が 1 時間先なら待機に入る", weeklyTurn(now.Add(time.Hour)), OutcomeWaiting},
			{"解除が 24 時間ちょうど先なら待機に入る", weeklyTurn(now.Add(maxWeeklyWait)), OutcomeWaiting},
			{"解除が 24 時間より先なら待たない", weeklyTurn(now.Add(maxWeeklyWait + time.Second)), OutcomeWeeklyCap},
			{"解除が 3 日先なら待たない", weeklyTurn(now.Add(72 * time.Hour)), OutcomeWeeklyCap},
			{"5 日前に踏んだ entry でも解除が 1 時間先なら待機に入る", datedTurn(weeklyTurn(now.Add(time.Hour)), now.Add(-5*24*time.Hour)), OutcomeWaiting},
			{"解除から 1 時間過ぎていれば即起床の待機に入る", weeklyTurn(now.Add(-time.Hour)), OutcomeWaiting},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				client := &multiplexerfake.Client{}
				ps := &PaneState{}
				outcome, err := Tick(context.Background(), newTestDeps(t, cfg, client, tt.turn), pane, ps, now)
				if err != nil || outcome != tt.want {
					t.Fatalf("Tick() = %v, %v, want %v", outcome, err, tt.want)
				}
				assertNoRead(t, client)
				if tt.want == OutcomeWeeklyCap {
					if *ps != (PaneState{}) {
						t.Errorf("ps = %+v, want 監視のまま", ps)
					}
					return
				}
				wantWake, _ := ComputeLimitWake(cfg, tt.turn.Entry, now)
				if ps.Status != StatusWaiting || ps.TurnUUID != "weekly" || !ps.WaitUntil.Equal(wantWake) {
					t.Errorf("ps = %+v, want waiting / uuid weekly / wake %v", ps, wantWake)
				}
			})
		}
	})

	t.Run("待機明けに解除を過ぎていればそのまま送る", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, cfg, client, datedTurn(weeklyTurn(now.Add(-time.Minute)), now.Add(-6*24*time.Hour)))
		ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), TurnUUID: "weekly"}
		outcome, err := Tick(context.Background(), deps, pane, ps, now)
		if err != nil || outcome != OutcomeRetried || ps.Attempts != 1 {
			t.Errorf("Tick() = %v, %v, ps = %+v; want retried / attempts 1", outcome, err, ps)
		}
	})

	t.Run("待機明けに別の entry で解除が 24 時間以内の未来なら待ち直す", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, cfg, client, weeklyTurn(now.Add(2*time.Hour)))
		ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 1, TurnUUID: uuidLimit}
		outcome, err := Tick(context.Background(), deps, pane, ps, now)
		if err != nil || outcome != OutcomeWaiting {
			t.Fatalf("Tick() = %v, %v, want waiting", outcome, err)
		}
		if ps.TurnUUID != "weekly" || ps.Attempts != 1 || !ps.WaitUntil.Equal(now.Add(2*time.Hour).Add(margin)) {
			t.Errorf("ps = %+v, want uuid weekly / attempts 1 / 解除 + margin", ps)
		}
		assertNoRead(t, client)
	})

	t.Run("待機明けに別の entry で解除が 24 時間より先なら監視へ戻る", func(t *testing.T) {
		client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
		deps := newTestDeps(t, cfg, client, weeklyTurn(now.Add(72*time.Hour)))
		ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 1, TurnUUID: uuidLimit}
		outcome, err := Tick(context.Background(), deps, pane, ps, now)
		if err != nil || outcome != OutcomeWeeklyCap || *ps != (PaneState{}) {
			t.Errorf("Tick() = %v, %v, ps = %+v; want weekly-cap / ゼロ値", outcome, err, ps)
		}
		assertNoRead(t, client)
	})
}

// 待っても解けない上限は待機に入らない。待機中に変わったら監視へ戻る。
func TestTick_CapsDoNotWait(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		turn Turn
		want Outcome
	}{
		{"Fable のモデル単位の上限", hardCapTurn(), OutcomeHardCap},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/監視中", func(t *testing.T) {
			client := &multiplexerfake.Client{}
			deps := newTestDeps(t, config.Default(), client, tt.turn)
			ps := &PaneState{}
			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)
			if err != nil || outcome != tt.want || ps.Status != StatusMonitoring {
				t.Errorf("Tick() = %v, %v, ps = %+v; want %v / monitoring", outcome, err, ps, tt.want)
			}
			assertNoRead(t, client)
		})
		t.Run(tt.name+"/待機中", func(t *testing.T) {
			client := &multiplexerfake.Client{PaneReadFunc: readReturning(cleanScreen, nil)}
			deps := newTestDeps(t, config.Default(), client, tt.turn)
			ps := &PaneState{Status: StatusWaiting, Attempts: 1, TurnUUID: uuidLimit}
			outcome, err := Tick(context.Background(), deps, newTestPane(multiplexer.AgentStatusError, true), ps, now)
			if err != nil || outcome != tt.want || *ps != (PaneState{}) {
				t.Errorf("Tick() = %v, %v, ps = %+v; want %v / ゼロ値", outcome, err, ps, tt.want)
			}
			assertNoSend(t, client)
		})
	}
}

// detect.Kind を直接参照して分類フィクスチャの前提が崩れていないことを確認する。
func TestFixtures_ClassifyAsExpected(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		turn Turn
		want detect.Kind
	}{
		{"limitedTurn", limitedTurn(uuidLimit, now), detect.KindLimit},
		{"legacyLimitedTurn", legacyLimitedTurn(now), detect.KindNone},
		{"weeklyTurn", weeklyTurn(now), detect.KindWeeklyLimit},
		{"transientTurn", transientTurn(now), detect.KindTransient},
		{"hardCapTurn", hardCapTurn(), detect.KindSpendLimit},
		{"resumedTurn", resumedTurn(), detect.KindNone},
	}
	for _, tt := range tests {
		if got := detect.ClassifyTurn(tt.turn.Entry); got != tt.want {
			t.Errorf("%s: ClassifyTurn = %v, want %v", tt.name, got, tt.want)
		}
	}
	screens := []struct {
		name   string
		screen string
		want   detect.Hint
	}{
		{"cleanScreen", cleanScreen, detect.HintNone},
		{"nativeArmedScreen", nativeArmedScreen, detect.HintNativeArmed},
		{"nativeRetryingScreen", nativeRetryingScreen, detect.HintNativeRetrying},
		{"resumeReadyScreen", resumeReadyScreen, detect.HintResumeReady},
		{"lowerPriorityScreen", lowerPriorityScreen, detect.HintLowerPriority},
	}
	for _, tt := range screens {
		if got := detect.ClassifyScreen(tt.screen, 0); got != tt.want {
			t.Errorf("%s: ClassifyScreen = %v, want %v", tt.name, got, tt.want)
		}
	}
}
