package monitor

// 状態機械の不変条件を、乱択の tick 列で確かめる。個々のテストが 1 場面ずつ固定するのに対し、ここでは
// 「どの transcript、どの画面、どの pane、どの時刻の並びでも破ってはいけないこと」を書く。反例が出たら seed と tick の
// 番号を出すので、同じ seed で再現できる。反例が出たらコードを直すか挙動を正当化する。性質を弱めて通さない。

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
)

const (
	traceCount = 600
	traceTicks = 80
)

// traceScreens は待機明けに読む画面。detect が返す全ての Hint と空画面を含む。
var traceScreens = []string{cleanScreen, "", nativeArmedScreen, nativeRetryingScreen, resumeReadyScreen, lowerPriorityScreen}

// randomTurn は transcript の末尾。読めない、detect が返す全ての Kind、同じ上限の別の entry、古い entry と時刻の無い entry を含む。
func randomTurn(r *rand.Rand, now time.Time) Turn {
	var turn Turn
	switch r.IntN(14) {
	case 0:
		return noSessionTurn
	case 1:
		return unreadableTurn
	case 2:
		return resumedTurn()
	case 3:
		turn = legacyLimitedTurn(now)
	case 4:
		turn = weeklyTurn(now.Add(time.Duration(r.IntN(96*3600)-3600) * time.Second))
	case 5:
		turn = transientTurn(now)
	case 6:
		turn = hardCapTurn()
	default:
		uuid := uuidLimit
		if r.IntN(3) == 0 {
			uuid = uuidLimit2
		}
		turn = limitedTurn(uuid, now.Add(time.Duration(r.IntN(12*3600)-6*3600)*time.Second))
	}
	// entry の時刻は今から 36 時間前までの間か、読めない (ゼロ値)。
	switch r.IntN(6) {
	case 0:
		return datedTurn(turn, time.Time{})
	default:
		return datedTurn(turn, now.Add(-time.Duration(r.IntN(36*3600))*time.Second))
	}
}

// step は 1 tick の入力。
type step struct {
	turn    Turn
	screen  string
	readErr error
	sendErr error
	pane    multiplexer.Pane
	rate    *ratelimit.State
	account *ratelimit.State
	now     time.Time
}

var (
	errFuzzRead = errors.New("read failed")
	errFuzzSend = errors.New("send failed")
)

func randomStep(r *rand.Rand, prev time.Time) step {
	advances := []time.Duration{0, time.Second, 10 * time.Second, 31 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}
	now := prev.Add(advances[r.IntN(len(advances))])
	st := step{
		turn:    randomTurn(r, now),
		screen:  traceScreens[r.IntN(len(traceScreens))],
		pane:    randomPane(r),
		rate:    randomRateState(r, now),
		account: randomRateState(r, now),
		now:     now,
	}
	if st.turn.Reason == noSessionTurn.Reason {
		st.pane.SessionID = ""
	}
	if r.IntN(10) == 0 {
		st.readErr = errFuzzRead
	}
	if r.IntN(10) == 0 {
		st.sendErr = errFuzzSend
	}
	return st
}

func randomPane(r *rand.Rand) multiplexer.Pane {
	agents := []string{"claude-code", "claude-code", "claude-code", "claude-code", "claude-code", "bash", ""}
	statuses := []multiplexer.AgentStatus{
		multiplexer.AgentStatusIdle, multiplexer.AgentStatusWorking, multiplexer.AgentStatusBlocked,
		multiplexer.AgentStatusDone, multiplexer.AgentStatusError, multiplexer.AgentStatusError, multiplexer.AgentStatusUnknown, "",
	}
	kinds := []string{"", "", multiplexer.AgentKindPermission, multiplexer.AgentKindQuestion, multiplexer.AgentKindAuth, "dialog"}
	return multiplexer.Pane{
		PaneID:      "pane-1",
		SessionID:   "sid-1",
		Agent:       agents[r.IntN(len(agents))],
		AgentStatus: statuses[r.IntN(len(statuses))],
		AgentKind:   kinds[r.IntN(len(kinds))],
	}
}

// randomRateState は state 無し、five_hour が null、過去または未来の resets_at を持つ state を引く。
func randomRateState(r *rand.Rand, now time.Time) *ratelimit.State {
	switch r.IntN(4) {
	case 0:
		return nil
	case 1:
		return &ratelimit.State{ObservedAt: now}
	default:
		return &ratelimit.State{
			FiveHourUsed:     r.Float64() * 100,
			FiveHourResetsAt: now.Add(time.Duration(r.IntN(12*3600)-6*3600) * time.Second),
			ObservedAt:       now.Add(-time.Duration(r.IntN(1800)) * time.Second),
		}
	}
}

func randomConfig(r *rand.Rand) config.Config {
	cfg := config.Default()
	cfg.MaxRetries = 1 + r.IntN(4)
	cfg.DeferToNativeAutoContinue = r.IntN(2) == 0
	cfg.HandleTransient = r.IntN(2) == 0
	cfg.DismissMenu = r.IntN(2) == 0
	return cfg
}

// tickResult は 1 tick の前後の状態と、その tick で起きたこと。
type tickResult struct {
	pre      PaneState
	post     PaneState
	outcome  Outcome
	err      error
	calls    []string
	turns    int
	observed Evidence
	seen     bool
}

func (tr tickResult) sent() bool {
	return slices.ContainsFunc(tr.calls, func(c string) bool {
		return strings.HasPrefix(c, "SendText:") || strings.HasPrefix(c, "SendKeys:")
	})
}

func (tr tickResult) sentText() bool {
	return slices.ContainsFunc(tr.calls, func(c string) bool { return strings.HasPrefix(c, "SendText:") })
}

func (tr tickResult) read() bool {
	return slices.ContainsFunc(tr.calls, func(c string) bool { return strings.HasPrefix(c, "PaneRead:") })
}

func TestTick_不変条件を乱択のtick列で破らない(t *testing.T) {
	// 乱択が状態機械の全ての経路を通ったことも見る。通っていない Outcome があれば、性質は何も確かめていない。
	reached := map[Outcome]bool{}
	defer func() {
		for outcome, name := range outcomeNames {
			if !reached[outcome] {
				t.Errorf("Outcome %s に一度も至らなかった (入力の生成を見直す)", name)
			}
		}
	}()
	for seed := range traceCount {
		r := rand.New(rand.NewPCG(uint64(seed), 0)) // #nosec G115 -- seed は非負
		cfg := randomConfig(r)
		var cur step
		var tr tickResult
		client := &multiplexerfake.Client{
			PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) {
				return cur.screen, cur.readErr
			},
			SendTextFunc: func(context.Context, string, string) error { return cur.sendErr },
			SendKeysFunc: func(context.Context, string, ...string) error { return cur.sendErr },
		}
		deps := Deps{
			Client: client, Config: cfg, Sleep: func(time.Duration) {},
			Turns:   func(string) Turn { tr.turns++; return cur.turn },
			Observe: func(_ multiplexer.Pane, ev Evidence) { tr.observed, tr.seen = ev, true },
		}
		ps := &PaneState{}
		now := time.Date(2026, 8, 28, 7, 35, 0, 0, time.UTC)
		for tick := range traceTicks {
			cur = randomStep(r, now)
			now = cur.now
			deps.Account = cur.account
			tr = tickResult{pre: *ps}
			before := len(client.Calls)
			tr.outcome, tr.err = Tick(context.Background(), deps, cur.pane, ps, cur.rate, now)
			tr.post, tr.calls = *ps, client.Calls[before:]
			reached[tr.outcome] = true
			if msg := checkTick(cfg, cur, tr); msg != "" {
				t.Fatalf("seed %d tick %d: %s\n  pane=%+v turn=%+v screen=%q readErr=%v\n  pre=%+v\n  post=%+v\n  outcome=%v err=%v calls=%v observed=%+v",
					seed, tick, msg, cur.pane, cur.turn, cur.screen, cur.readErr, tr.pre, tr.post, tr.outcome, tr.err, tr.calls, tr.observed)
			}
		}
	}
}

// checkTick は 1 tick の不変条件を見て、破れていればその説明を返す。
func checkTick(cfg config.Config, st step, tr tickResult) string {
	if msg := checkRead(st, tr); msg != "" {
		return msg
	}
	if msg := checkSend(cfg, st, tr); msg != "" {
		return msg
	}
	return checkState(st, tr)
}

// checkRead は読む順に関する不変条件。画面を読むのは待機明けで transcript が上限のときだけ。
func checkRead(st step, tr tickResult) string {
	woke := tr.pre.Status == StatusWaiting && !st.now.Before(tr.pre.WaitUntil)
	switch {
	case tr.outcome == OutcomeStillWaiting && (tr.turns != 0 || tr.read() || tr.seen):
		return "期限前なのに transcript か画面を読んだ"
	case tr.turns > 1:
		return "transcript を 1 tick で 2 回以上読んだ"
	case tr.read() && tr.pre.Status != StatusWaiting:
		return "監視中に画面を読んだ (検知は transcript だけで決める)"
	case tr.read() && !woke:
		return "期限前に画面を読んだ"
	case tr.read() && st.pane.AgentStatus == multiplexer.AgentStatusWorking:
		return "working の pane の画面を読んだ"
	case tr.read() && !tr.observed.Kind.Limited():
		return "transcript が上限でないのに画面を読んだ"
	case tr.read() && (!multiplexer.IsClaudeAgent(st.pane) || multiplexer.WaitingOnPrompt(st.pane)):
		return "Claude でない、または prompt の開いた pane の画面を読んだ"
	}
	return ""
}

// checkSend は送信に関する不変条件。送ってはいけない場面で送らないこと。
func checkSend(cfg config.Config, st step, tr tickResult) string {
	if !tr.sent() {
		return ""
	}
	switch {
	case tr.pre.Status != StatusWaiting || st.now.Before(tr.pre.WaitUntil):
		return "待機の期限前、または監視中に送った"
	case st.pane.AgentStatus == multiplexer.AgentStatusWorking:
		return "working の pane へ送った"
	case !multiplexer.IsClaudeAgent(st.pane):
		return "Claude agent でない pane へ送った"
	case multiplexer.WaitingOnPrompt(st.pane):
		return "利用者の答えを待つ prompt が開いている pane へ送った"
	case st.turn.Entry == nil || !tr.observed.Kind.Limited():
		return "transcript の末尾が上限 (か一時エラー) でないのに送った"
	case tr.observed.Kind == detect.KindTransient && !cfg.HandleTransient:
		return "handleTransient が off なのに一時エラーへ送った"
	case !tr.observed.ScreenRead || strings.TrimSpace(st.screen) == "" || st.readErr != nil:
		return "画面を読めていないのに送った"
	case tr.observed.Hint == detect.HintNativeRetrying || tr.observed.Hint == detect.HintLowerPriority:
		return "手を出してはいけない画面 (" + tr.observed.Hint.String() + ") へ送った"
	case tr.observed.Hint == detect.HintNativeArmed && cfg.DeferToNativeAutoContinue:
		return "auto-continue の予約中に送った"
	case tr.observed.Hint == detect.HintResumeReady && tr.sentText():
		return "Enter 待ちの画面へテキストを送った (新しい依頼になる)"
	case tr.observed.Kind == detect.KindLimit && tr.pre.TurnUUID != st.turn.Entry.UUID && st.turn.Entry.Quota != nil && st.turn.Entry.Quota.ResetsAt.After(st.now):
		return "新しい上限の entry の解除時刻が未来なのに送った"
	case (tr.observed.Kind != detect.KindTransient || tr.observed.Hint == detect.HintResumeReady) && tr.pre.Attempts >= cfg.MaxRetries:
		return "maxRetries に達した後も送った"
	case tr.post.Attempts != tr.pre.Attempts+1:
		return "送ったのに Attempts が 1 増えていない"
	}
	return ""
}

// checkState は状態と Outcome の整合に関する不変条件。
func checkState(st step, tr tickResult) string {
	monitoringOutcomes := []Outcome{
		OutcomeMonitoring, OutcomeWorkingSuppressed, OutcomeRecoveredByUser, OutcomeStaleTurn,
		OutcomeNativeAutoContinue, OutcomeLowerPriority, OutcomeHardCap, OutcomeWeeklyCap,
	}
	switch {
	case tr.post.Status == StatusMonitoring && (tr.post.Attempts != 0 || tr.post.TurnUUID != ""):
		return "監視中なのに Attempts か TurnUUID が残っている"
	case tr.post.Status == StatusWaiting && tr.post.TurnUUID == "":
		return "待機中なのに根拠の entry の uuid が無い"
	case tr.pre.Status == StatusMonitoring && tr.post.Status == StatusWaiting && StaleTurn(st.turn.Entry, st.now):
		return "24 時間より古い (か時刻の無い) entry で待機に入った"
	case tr.outcome == OutcomeStaleTurn && (tr.post.Status != StatusMonitoring || tr.sent() || !StaleTurn(st.turn.Entry, st.now)):
		return "stale-turn なのに待機に入ったか送ったか、entry が古くない"
	case tr.outcome == OutcomeStillWaiting && (len(tr.calls) != 0 || tr.post != tr.pre):
		return "期限前なのに画面を読んだか状態を変えた"
	case slices.Contains(monitoringOutcomes, tr.outcome) && (tr.post.Status != StatusMonitoring || tr.sent()):
		return "監視へ戻る Outcome なのに待機のままか送った"
	case (tr.outcome == OutcomeNoSession || tr.outcome == OutcomeTranscriptUnavailable) && (tr.post.Status != tr.pre.Status || tr.post.Attempts != tr.pre.Attempts || tr.sent()):
		return "判定できない tick で状態を変えたか送った"
	case tr.outcome == OutcomeReadError && tr.post != tr.pre:
		return "画面を読めなかった tick で状態を変えた"
	case tr.post.Status == StatusWaiting && tr.outcome != OutcomeStillWaiting && tr.outcome != OutcomeReadError && tr.post.WaitUntil.Before(st.now):
		return "待機に入った (続けた) のに期限が過去"
	case tr.err != nil && tr.outcome != OutcomeReadError && tr.outcome != OutcomeSendError:
		return "error を返したのに Outcome が失敗を表していない"
	case tr.outcome == OutcomeWorkingSuppressed && st.pane.AgentStatus != multiplexer.AgentStatusWorking:
		return "working でない pane に suppressed-working を返した"
	}
	return ""
}

func TestComputeLimitWake_不変条件(t *testing.T) {
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, tokyo)
	for seed := range 3000 {
		r := rand.New(rand.NewPCG(uint64(seed), 1)) // #nosec G115 -- seed は非負
		cfg := config.Default()
		cfg.MarginSeconds = r.IntN(600)
		cfg.FallbackWaitHours = r.Float64() * 6
		margin := time.Duration(cfg.MarginSeconds) * time.Second
		now := base.Add(time.Duration(r.IntN(48*3600)) * time.Second)
		own, account := randomRateState(r, now), randomRateState(r, now)
		entry := randomTurn(r, now).Entry

		wake, src := ComputeLimitWake(cfg, entry, own, account, now)

		fail := func(msg string) {
			t.Fatalf("seed %d: %s\n  now=%v entry=%+v own=%+v account=%+v\n  wake=%v source=%v", seed, msg, now, entry, own, account, wake, src)
		}
		hasQuota := entry != nil && entry.Quota != nil && !entry.Quota.ResetsAt.IsZero()
		switch {
		case wake.Before(now.Add(margin)):
			fail("now + margin より前に起こす")
		case hasQuota && entry.Quota.ResetsAt.After(now) && (src != WakeFromTranscript || !wake.Equal(entry.Quota.ResetsAt.Add(margin))):
			fail("transcript の resetsAt が未来なのに採らなかった")
		case hasQuota && !entry.Quota.ResetsAt.After(now) && (src != WakeTranscriptAlreadyReset || !wake.Equal(now.Add(margin))):
			fail("transcript の resetsAt が過去なのに即起床しなかった")
		case src == WakeFromFallback && (hasQuota || own.HasFiveHour() || account.HasFiveHour()):
			fail("解除時刻の情報源があるのに fallback へ落ちた")
		case src == WakeFromFallback && !wake.Equal(now.Add(time.Duration(cfg.FallbackWaitHours*float64(time.Hour))).Add(margin)):
			fail("fallback の起床時刻が FallbackWaitHours + margin でない")
		}
	}
}

func TestTransientBackoff_単調で上限を超えない(t *testing.T) {
	for seed := range 500 {
		r := rand.New(rand.NewPCG(uint64(seed), 2)) // #nosec G115 -- seed は非負
		cfg := config.Default()
		cfg.TransientWaitSeconds = r.IntN(3601)
		cfg.TransientMaxWaitSeconds = r.IntN(7201)
		capped := time.Duration(cfg.TransientMaxWaitSeconds) * time.Second
		prev := time.Duration(0)
		for attempts := range 101 {
			got := transientBackoff(attempts, cfg)
			if got < 0 || got > capped {
				t.Fatalf("seed %d (base %ds, cap %ds): attempts=%d backoff=%v が [0, cap] の外", seed, cfg.TransientWaitSeconds, cfg.TransientMaxWaitSeconds, attempts, got)
			}
			if got < prev {
				t.Fatalf("seed %d (base %ds, cap %ds): attempts=%d backoff=%v < 前回 %v", seed, cfg.TransientWaitSeconds, cfg.TransientMaxWaitSeconds, attempts, got, prev)
			}
			prev = got
		}
	}
}
