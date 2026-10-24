package monitor

// 状態機械の不変条件を、乱択の tick 列で確かめる。個々のテストが 1 場面ずつ固定するのに対し、ここでは
// 「どの画面、どの pane、どの時刻の並びでも破ってはいけないこと」を書く。反例が出たら seed と tick の番号を
// 出すので、同じ seed で再現できる。反例が出たらコードを直すか挙動を正当化する。性質を弱めて通さない。

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
)

const (
	traceCount = 300
	traceTicks = 60
)

// traceScreens は tick ごとに引く画面。detect が返す全ての Kind と空画面を含む。
var traceScreens = []string{
	cleanScreen, "", limitAndRelativeScreen, limitAbsoluteScreen, limitAmPmScreen, transientScreen,
	nativeArmedScreen, resumeReadyScreen, hardCapScreen,
}

// step は 1 tick の入力。
type step struct {
	screen  string
	readErr error
	pane    multiplexer.Pane
	rate    *ratelimit.State
	account *ratelimit.State
	now     time.Time
}

var errFuzzRead = errors.New("read failed")

func randomStep(r *rand.Rand, prev time.Time) step {
	advances := []time.Duration{0, time.Second, 10 * time.Second, 31 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}
	now := prev.Add(advances[r.IntN(len(advances))])
	st := step{
		screen:  traceScreens[r.IntN(len(traceScreens))],
		pane:    randomPane(r),
		rate:    randomRateState(r, now),
		account: randomRateState(r, now),
		now:     now,
	}
	if r.IntN(10) == 0 {
		st.readErr = errFuzzRead
	}
	return st
}

func randomPane(r *rand.Rand) multiplexer.Pane {
	agents := []string{"claude-code", "claude-code", "claude-code", "bash", ""}
	statuses := []multiplexer.AgentStatus{
		multiplexer.AgentStatusIdle, multiplexer.AgentStatusWorking, multiplexer.AgentStatusBlocked,
		multiplexer.AgentStatusDone, multiplexer.AgentStatusError, multiplexer.AgentStatusUnknown, "",
	}
	kinds := []string{"", "", kindPermission, kindQuestion, kindAuth, "dialog"}
	return multiplexer.Pane{
		PaneID:      "pane-1",
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
	observed detect.Kind
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

func TestTick_不変条件を乱択のtick列で破らない(t *testing.T) {
	for seed := range traceCount {
		r := rand.New(rand.NewPCG(uint64(seed), 0)) // #nosec G115 -- seed は非負
		cfg := randomConfig(r)
		var cur step
		client := &multiplexerfake.Client{
			PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) {
				return cur.screen, cur.readErr
			},
		}
		var tr tickResult
		deps := newTestDeps(t, cfg, client)
		deps.Observe = func(_ multiplexer.Pane, kind detect.Kind, _ string) {
			tr.observed, tr.seen = kind, true
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
			if msg := checkTick(cfg, cur, tr); msg != "" {
				t.Fatalf("seed %d tick %d: %s\n  pane=%+v screen=%q readErr=%v\n  pre=%+v\n  post=%+v\n  outcome=%v err=%v calls=%v observed=%v",
					seed, tick, msg, cur.pane, cur.screen, cur.readErr, tr.pre, tr.post, tr.outcome, tr.err, tr.calls, tr.observed)
			}
		}
	}
}

// checkTick は 1 tick の不変条件を見て、破れていればその説明を返す。
func checkTick(cfg config.Config, st step, tr tickResult) string {
	if msg := checkSend(cfg, st, tr); msg != "" {
		return msg
	}
	return checkState(st, tr)
}

// checkSend は送信に関する不変条件。送ってはいけない場面で送らないこと。
func checkSend(cfg config.Config, st step, tr tickResult) string {
	if !tr.sent() {
		return ""
	}
	switch {
	case tr.pre.Status != StatusWaiting || st.now.Before(tr.pre.WaitUntil):
		return "待機の期限前、または監視中に送った"
	case !multiplexer.IsClaudeAgent(st.pane):
		return "Claude agent でない pane へ送った"
	case waitingOnPrompt(st.pane):
		return "利用者の答えを待つ prompt が開いている pane へ送った"
	case !tr.seen || strings.TrimSpace(st.screen) == "":
		return "画面を読めていないのに送った"
	case tr.observed == detect.KindAutoContinueArmed || tr.observed == detect.KindSpendLimit:
		return "手を出してはいけない画面 (" + tr.observed.String() + ") へ送った"
	case tr.observed == detect.KindResumeReady && tr.sentText():
		return "Enter 待ちの画面へテキストを送った (新しい依頼になる)"
	case tr.observed == detect.KindTransient && !cfg.HandleTransient:
		return "handleTransient が off なのに一時エラーへ送った"
	case tr.observed == detect.KindNone:
		return "上限でも一時エラーでもない画面へ送った"
	case tr.observed != detect.KindTransient && tr.pre.Attempts >= cfg.MaxRetries:
		return "maxRetries に達した後も送った"
	case tr.post.Attempts != tr.pre.Attempts+1:
		return "送ったのに Attempts が 1 増えていない"
	}
	return ""
}

// checkState は状態と Outcome の整合に関する不変条件。
func checkState(st step, tr tickResult) string {
	monitoringOutcomes := []Outcome{
		OutcomeMonitoring, OutcomeGateSuppressed, OutcomeRecoveredByUser,
		OutcomeNativeAutoContinue, OutcomeHardCap, OutcomeReadError,
	}
	switch {
	case tr.post.Status == StatusMonitoring && tr.post.Attempts != 0:
		return "監視中なのに Attempts が残っている"
	case tr.outcome == OutcomeStillWaiting && (len(tr.calls) != 0 || tr.post != tr.pre):
		return "期限前なのに画面を読んだか状態を変えた"
	case slices.Contains(monitoringOutcomes, tr.outcome) && (tr.post.Status != StatusMonitoring || tr.sent()):
		return "監視へ戻る Outcome なのに待機のままか送った"
	case tr.post.Status == StatusWaiting && tr.outcome != OutcomeStillWaiting && tr.outcome != OutcomeScreenUnreadable && tr.post.WaitUntil.Before(st.now):
		return "待機に入った (続けた) のに期限が過去"
	case tr.err != nil && tr.outcome != OutcomeReadError && tr.outcome != OutcomeScreenUnreadable && tr.outcome != OutcomeSendError:
		return "error を返したのに Outcome が失敗を表していない"
	}
	return ""
}

func TestComputeLimitWake_不変条件(t *testing.T) {
	screens := []string{limitAmPmScreen, limitAndRelativeScreen, limitAbsoluteScreen, cleanScreen, ""}
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, tokyo)
	for seed := range 3000 {
		r := rand.New(rand.NewPCG(uint64(seed), 1)) // #nosec G115 -- seed は非負
		cfg := config.Default()
		cfg.MarginSeconds = r.IntN(600)
		cfg.FallbackWaitHours = r.Float64() * 6
		margin := time.Duration(cfg.MarginSeconds) * time.Second
		now := base.Add(time.Duration(r.IntN(48*3600)) * time.Second)
		own, account := randomRateState(r, now), randomRateState(r, now)
		screen := screens[r.IntN(len(screens))]

		wake, src := computeLimitWakeIn(cfg, own, account, screen, now, tokyo)

		fail := func(msg string) {
			t.Fatalf("seed %d: %s\n  now=%v own=%+v account=%+v screen=%q\n  wake=%v source=%v", seed, msg, now, own, account, screen, wake, src)
		}
		switch {
		case wake.Before(now.Add(margin)):
			fail("now + margin より前に起こす")
		case own.HasFiveHour() && own.FiveHourResetsAt.After(now) && (src != WakeFromResetsAt || !wake.Equal(own.FiveHourResetsAt.Add(margin))):
			fail("own の resets_at が未来なのに採らなかった")
		case src == WakeFromFallback && (own.HasFiveHour() || account.HasFiveHour()):
			fail("state があるのに fallback へ落ちた")
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
