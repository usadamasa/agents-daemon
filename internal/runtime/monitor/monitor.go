// Package monitor は多重化ツールの pane 1 枚ぶんの状態機械を実装する。
//
// 「monitoring (通常監視)」と「waiting (上限解除 / 一時エラー待ち)」の 2 状態を持ち、
// 呼び出し元 (daemon) が pane ごとに *PaneState を保持して Tick を毎ポーリング呼び出す
// 想定である。このパッケージ自身は PaneList を呼ばない。pane の発見・列挙は
// daemon の責務であり、ここでは渡された 1 pane を判断するだけに徹する。
//
// 上限かどうかは、そのセッションの transcript の末尾の会話の entry (detect.ClassifyTurn) で決める。画面の文字列では
// 決めない。pane の画面には tool の出力に引用された任意の文章が並び、本物の上限の文と見分けられない (#257)。
// 判定の順は次のとおり。
//
//  1. pane の session_id が無い、transcript が読めない → 検知の対象外 (理由を Observe に渡す)
//  2. record (OSC 7501) が working → 何もしない。turn の途中、retry watchdog の待機中、lower-priority mode の作業中は
//     どれも working で申告される。transcript が上限なのに working なら、その食い違いを記録する
//  3. transcript の末尾が上限 (5 時間ウィンドウ) なら、その entry の resetsAt + margin まで待つ。一時エラーなら短い
//     バックオフ。週の上限と待っても解けない上限は待たず記録だけ残す。entry が 24 時間より古い (daemon の起動前や
//     `claude --resume` の前に止まった) ものも待たず記録だけ残す
//  4. 待機明けに transcript がまだ同じ上限なら、画面を 1 回だけ読んで送るキーを選ぶ (detect.ClassifyScreen)。
//     本体がリトライ中・auto-continue の予約中・lower-priority で作業中なら手を引き、解除済みで Enter 待ちなら Enter だけ、
//     どれでもなければ esc → retryMessage → enter
//
// 参考実装は https://github.com/mo-arvan/herdr-claude-auto-retry の src/monitor-core.js (processOneTick)。
// 状態機械の形 (待機 → 送信 → maxRetries で打ち切り、transient の指数バックオフ) はそこから借りている。
package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// Status は pane 1 枚の状態機械が取りうる状態。
type Status int

const (
	// StatusMonitoring は通常監視中。ゼロ値なので PaneState{} の初期状態になる。
	StatusMonitoring Status = iota
	// StatusWaiting は上限解除または一時エラーの回復を待っている状態。
	StatusWaiting
)

// PaneState は pane 1 枚ぶんの状態機械の内部状態。daemon が pane ごとに保持し、
// Tick へポインタで渡す。ゼロ値がそのまま「監視中・未送信」の初期状態になる。
type PaneState struct {
	Status    Status
	WaitUntil time.Time
	Attempts  int
	// TurnUUID は待機に入る根拠にした transcript の entry の uuid。待機明けに別の uuid の上限の entry があれば、
	// 待っている間に新しい依頼 (peer の SendMessage など) がまた上限に当たったので、その解除時刻で待ち直す。
	TurnUUID string
}

// Outcome は Tick が 1 tick ぶんに行った (あるいは行わなかった) ことを表す。
// daemon はこれをログに出すために使う。
type Outcome int

const (
	// OutcomeStillWaiting は待機中でまだ起床時刻に達していないため何もしなかったことを表す。
	OutcomeStillWaiting Outcome = iota
	// OutcomeMonitoring は監視中で異常が無かったことを表す。
	OutcomeMonitoring
	// OutcomeNoSession は pane の session_id が引けず、検知の対象外だったことを表す。
	OutcomeNoSession
	// OutcomeTranscriptUnavailable は session は分かるが transcript の末尾を読めず、判定できなかったことを表す。
	// 待機中なら待機を続ける (読めないことは再開の証拠にならない)。
	OutcomeTranscriptUnavailable
	// OutcomeWorkingSuppressed は transcript の末尾が上限なのに record が working で、手を出さなかったことを表す。
	// 書き込みの遅れか、「上限で止まった pane は working でなくなる」前提が外れたかの実測になる。
	OutcomeWorkingSuppressed
	// OutcomeStaleTurn は transcript の末尾が上限か一時エラーだが entry が maxTurnAge より古い (か時刻が読めない) ため、
	// 待機に入らなかったことを表す。何日も前に止まったまま開いている pane や、`claude --resume` で戻した会話に送らない。
	OutcomeStaleTurn
	// OutcomeRecoveredByUser は待機中にセッションが上限から戻った (次の turn が始まった、record が working になった) と
	// 判断し、送信せず監視状態へ戻したことを表す。
	OutcomeRecoveredByUser
	// OutcomeNotClaudeAgent は待機中に pane が Claude agent でなくなったため、
	// 送信を諦めて長めのバックオフに入ったことを表す。
	OutcomeNotClaudeAgent
	// OutcomeMaxRetries は再試行回数が上限に達し、送信せず長いクールダウンに
	// 入ったことを表す。
	OutcomeMaxRetries
	// OutcomeRetried は再開メッセージを送信し、待機を継続したことを表す
	// (通常の上限待ちと transient の両方で使う)。
	OutcomeRetried
	// OutcomeWaiting は監視中から待機状態へ入った、または待機中に新しい上限の entry で待ち直したことを表す。
	OutcomeWaiting
	// OutcomeReadError は待機明けの画面の読み取りに失敗 (エラーか空) し、送らず待機を続けたことを表す。
	OutcomeReadError
	// OutcomeSendError は待機中の回復送信 (esc/送信/enter) の途中でエラーが
	// 発生したことを表す。状態遷移自体は行われている。
	OutcomeSendError
	// OutcomeNativeAutoContinue は Claude Code 自身が解除を待っている (auto-continue の予約か request の中のリトライ)
	// ため、このツールが手を引いたことを表す。
	OutcomeNativeAutoContinue
	// OutcomeLowerPriority は lower-priority mode で作業中の通知が画面にあり、手を引いたことを表す (#251)。
	OutcomeLowerPriority
	// OutcomeResumeEnter は上限が解除済みで Enter 待ちの pane へ Enter を
	// 送ったことを表す。
	OutcomeResumeEnter
	// OutcomeHardCap は待っても解除されない上限 (spend limit / usage credit / Fable のモデル単位の上限) を
	// 検知し、待機に入らなかったことを表す。
	OutcomeHardCap
	// OutcomeWeeklyCap は週のウィンドウの上限を検知し、待機に入らなかったことを表す。解除は数日先になりうる (#338)。
	OutcomeWeeklyCap
	// OutcomeBlockedByPrompt は待機明けに Claude Code が利用者の答えを待つ prompt (permission / question /
	// auth) を開いていたため、送らずに待機を延長したことを表す。
	OutcomeBlockedByPrompt
)

// outcomeNames は Outcome のログ向けの識別子。
var outcomeNames = map[Outcome]string{
	OutcomeStillWaiting:          "still-waiting",
	OutcomeMonitoring:            "monitoring",
	OutcomeNoSession:             "no-session",
	OutcomeTranscriptUnavailable: "transcript-unavailable",
	OutcomeWorkingSuppressed:     "suppressed-working",
	OutcomeStaleTurn:             "stale-turn",
	OutcomeRecoveredByUser:       "recovered-by-user",
	OutcomeNotClaudeAgent:        "not-claude-agent",
	OutcomeMaxRetries:            "max-retries",
	OutcomeRetried:               "retried",
	OutcomeWaiting:               "waiting",
	OutcomeReadError:             "read-error",
	OutcomeSendError:             "send-error",
	OutcomeNativeAutoContinue:    "native-auto-continue",
	OutcomeLowerPriority:         "lower-priority",
	OutcomeResumeEnter:           "resume-enter",
	OutcomeHardCap:               "hard-cap",
	OutcomeWeeklyCap:             "weekly-cap",
	OutcomeBlockedByPrompt:       "blocked-by-prompt",
}

// String は Outcome をログ向けの識別子文字列にする。
func (o Outcome) String() string {
	if name, ok := outcomeNames[o]; ok {
		return name
	}
	return "unknown"
}

// 以下は参考実装 (monitor-core.js) が config に無い値として直書きしている定数を、
// そのまま同じ倍率で移植したもの。個別の設定項目にする理由が無いため定数のままにする。
const (
	// notClaudeBackoffMultiplier は「pane が Claude agent でなくなった」「prompt が開いている」「transcript を読めない」
	// ときの再チェック間隔を PollIntervalSeconds の何倍にするか。
	notClaudeBackoffMultiplier = 6
	// maxRetriesCooldownMultiplier は再試行上限到達後のクールダウンを
	// PollIntervalSeconds の何倍にするか。
	maxRetriesCooldownMultiplier = 12
	// retryWaitAfterSend は通常の (transient でない) 再送信後、次に画面を
	// 確認するまでの待ち時間。
	retryWaitAfterSend = 30 * time.Second
	// maxTurnAge は監視から待機へ入るときに entry に許す古さ。これより前に止まった pane は daemon が見ていなかった間に
	// 止まったもの (daemon の起動前、`claude --resume` で戻した会話) で、利用者が戻るのを待つ。本体の auto-continue が
	// 待つ上限 (解除が 24 時間より先なら降りる) と同じ線。見るのは待機に入る時点だけで、待機明けには見ない
	// (待っている間に 24 時間を越える経路を止めない)。
	maxTurnAge = 24 * time.Hour
)

// StaleTurn は entry が maxTurnAge より古いか、時刻が読めないかを返す。inspect が同じ判定を説明するのに使う。
func StaleTurn(e *transcript.Entry, now time.Time) bool {
	return e == nil || e.Timestamp.IsZero() || now.Sub(e.Timestamp) > maxTurnAge
}

// Client は Tick が pane に対して使う操作。多重化ツールの全部入りの multiplexer.Client のうち、画面を読む・送る
// の 3 つだけを要る側で切り出したもの (#301)。daemon はこれに列挙・端末の大きさ・到達性を足した自分の interface を持ち、
// dry-run や simulate の包みは送信の 2 つを握りつぶすだけでよい。rexcli の実装と multiplexerfake.Client が満たす。
type Client interface {
	// PaneRead は pane の画面テキストを返す。
	PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error)
	// SendText は pane へリテラルテキストを送る (Enter は押さない)。
	SendText(ctx context.Context, paneID, text string) error
	// SendKeys は pane へキー入力を送る (例: "esc", "enter")。
	SendKeys(ctx context.Context, paneID string, keys ...string) error
}

// Turn はセッションの transcript の末尾の会話の entry。Entry が nil なら Reason に読めなかった理由 (transcript.Tail と同じ形)。
type Turn = transcript.Tail

// Evidence は Tick が判定に使った根拠。daemon が上限の出来事の記録 (daemon/limitlog) に写す。判定には関わらない。
type Evidence struct {
	// Turn は読んだ transcript の末尾。
	Turn Turn
	// Kind は Turn の分類。
	Kind detect.Kind
	// ScreenRead は画面を読んだか (待機明けだけ)。読んだときの画面と通知欄の判定が Screen と Hint。
	ScreenRead bool
	Screen     string
	Hint       detect.Hint
}

// Deps は状態遷移に必要な依存をまとめたもの。テストでは Client に
// multiplexerfake.Client を、Sleep に記録用のダミー関数を注入する。
type Deps struct {
	Client Client
	Config config.Config
	// Sleep は recover 中の一時停止に使う。本番では time.Sleep を渡す想定。
	// テストは実際に待たないよう no-op またはログ記録用の関数を渡す。
	Sleep func(time.Duration)
	// Turns は session の transcript の末尾を読む。daemon は transcript.Store.Tail を渡す。
	Turns func(sessionID string) Turn
	// Account はアカウント全体で最も新しい 5 時間ウィンドウの statusline の state (ratelimit.Store.LatestWindow)。
	// transcript の entry に解除時刻が無い (quotaLimits の無い古い形) ときの起床時刻の予備。無ければ nil。
	Account *ratelimit.State
	// Observe は判定の根拠を受け取る (nil なら呼ばない)。起床前の tick では呼ばない。
	Observe func(pane multiplexer.Pane, ev Evidence)
}

// Tick は pane 1 枚ぶんの 1 tick を処理し、ps を必要に応じて書き換える。
//
// rateState は statusline が書く、この pane のセッションぶんの利用上限 state。transcript の entry に解除時刻が無いときの
// 起床時刻の予備で、無ければ nil を渡してよい。
//
// Client の呼び出しがエラーを返しても panic や状態破壊はしない。エラーは
// Outcome と共に返すので、呼び出し元は連続失敗のカウント等に使うこと。
func Tick(ctx context.Context, deps Deps, pane multiplexer.Pane, ps *PaneState, rateState *ratelimit.State, now time.Time) (Outcome, error) {
	if ps.Status == StatusWaiting && now.Before(ps.WaitUntil) {
		return OutcomeStillWaiting, nil
	}

	ev := Evidence{Turn: deps.Turns(pane.SessionID)}
	ev.Kind = detect.ClassifyTurn(ev.Turn.Entry)
	observe := func() {
		if deps.Observe != nil {
			deps.Observe(pane, ev)
		}
	}

	if ev.Turn.Entry == nil {
		observe()
		return unavailable(deps, pane, ps, now), nil
	}

	if pane.AgentStatus == multiplexer.AgentStatusWorking {
		observe()
		return working(ps, ev.Kind), nil
	}

	if ps.Status == StatusWaiting {
		outcome, err := tickWaiting(ctx, deps, pane, ps, &ev, rateState, now)
		observe()
		return outcome, err
	}
	observe()
	return tickMonitoring(deps, ps, ev, rateState, now), nil
}

// unavailable は session_id が無い、または transcript を読めない pane の tick。監視中なら何もせず、待機中なら
// 読めないことを再開の証拠にせず待機を延長する。
func unavailable(deps Deps, pane multiplexer.Pane, ps *PaneState, now time.Time) Outcome {
	if ps.Status == StatusWaiting {
		ps.WaitUntil = now.Add(time.Duration(deps.Config.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
	}
	if pane.SessionID == "" {
		return OutcomeNoSession
	}
	return OutcomeTranscriptUnavailable
}

// working は record が working の pane の tick。作業中の pane には絶対に割り込まない。待機中だったなら、
// 誰か (利用者、peer、本体の auto-continue) が再開したので監視へ戻る。
func working(ps *PaneState, kind detect.Kind) Outcome {
	if ps.Status == StatusWaiting {
		standDown(ps)
		return OutcomeRecoveredByUser
	}
	if kind != detect.KindNone {
		return OutcomeWorkingSuppressed
	}
	return OutcomeMonitoring
}

// standDown は待機中だった pane を監視状態へ戻す。
func standDown(ps *PaneState) {
	if ps.Status != StatusWaiting {
		return
	}
	*ps = PaneState{}
}

// tickMonitoring は監視状態での 1 tick を処理する。上限でも一時エラーでもない turn には何もしない。
func tickMonitoring(deps Deps, ps *PaneState, ev Evidence, rateState *ratelimit.State, now time.Time) Outcome {
	switch ev.Kind {
	case detect.KindSpendLimit:
		return OutcomeHardCap
	case detect.KindWeeklyLimit:
		// 解除が数日先になりうる。待たずに記録だけ残す (#338)。
		return OutcomeWeeklyCap
	case detect.KindTransient:
		if !deps.Config.HandleTransient {
			return OutcomeMonitoring
		}
		if StaleTurn(ev.Turn.Entry, now) {
			return OutcomeStaleTurn
		}
		ps.Status = StatusWaiting
		ps.TurnUUID = ev.Turn.Entry.UUID
		ps.WaitUntil = now.Add(time.Duration(deps.Config.TransientWaitSeconds) * time.Second)
		return OutcomeWaiting
	case detect.KindLimit:
		if StaleTurn(ev.Turn.Entry, now) {
			return OutcomeStaleTurn
		}
		ps.Status = StatusWaiting
		ps.TurnUUID = ev.Turn.Entry.UUID
		ps.WaitUntil, _ = ComputeLimitWake(deps.Config, ev.Turn.Entry, rateState, deps.Account, now)
		return OutcomeWaiting
	default:
		return OutcomeMonitoring
	}
}

// tickWaiting は待機明けの 1 tick を処理する。transcript がまだ上限なら画面を読んでキーを選ぶ。
func tickWaiting(ctx context.Context, deps Deps, pane multiplexer.Pane, ps *PaneState, ev *Evidence, rateState *ratelimit.State, now time.Time) (Outcome, error) {
	switch ev.Kind {
	case detect.KindNone:
		standDown(ps)
		return OutcomeRecoveredByUser, nil
	case detect.KindSpendLimit:
		standDown(ps)
		return OutcomeHardCap, nil
	case detect.KindWeeklyLimit:
		standDown(ps)
		return OutcomeWeeklyCap, nil
	case detect.KindTransient:
		if !deps.Config.HandleTransient {
			// 待っている間に上限から一時エラーへ変わった。設定で扱わないものなので本体と利用者に任せる。
			standDown(ps)
			return OutcomeRecoveredByUser, nil
		}
	}

	backoff := time.Duration(deps.Config.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second
	if !multiplexer.IsClaudeAgent(pane) {
		ps.WaitUntil = now.Add(backoff)
		return OutcomeNotClaudeAgent, nil
	}
	// 上限のまま permission prompt などが開いていることがある。esc は prompt を断り、Enter は承認するので、
	// 利用者が答えるまで送らない。
	if multiplexer.WaitingOnPrompt(pane) {
		ps.WaitUntil = now.Add(backoff)
		return OutcomeBlockedByPrompt, nil
	}

	entry := ev.Turn.Entry
	if ev.Kind == detect.KindLimit && entry.UUID != ps.TurnUUID {
		// 待っている間に新しい依頼がまた上限に当たった。解除時刻が先に移っていれば送らず待ち直す。試行回数は持ち越す。
		if entry.Quota != nil && entry.Quota.ResetsAt.After(now) {
			ps.TurnUUID = entry.UUID
			ps.WaitUntil, _ = ComputeLimitWake(deps.Config, entry, rateState, deps.Account, now)
			return OutcomeWaiting, nil
		}
	}
	return sendAtWake(ctx, deps, pane, ps, ev, now)
}

// sendAtWake は待機明けの送信。画面を 1 回読んで本体の通知欄からキーを選び、送る。
func sendAtWake(ctx context.Context, deps Deps, pane multiplexer.Pane, ps *PaneState, ev *Evidence, now time.Time) (Outcome, error) {
	screen, err := deps.Client.PaneRead(ctx, pane.PaneID, multiplexer.PaneReadOptions{Lines: deps.Config.ReadLines})
	if err != nil {
		return OutcomeReadError, fmt.Errorf("pane %s の読み取りに失敗: %w", pane.PaneID, err)
	}
	if strings.TrimSpace(screen) == "" {
		// 空の画面は capture の失敗で、キーを選べない。次の tick で読み直す。
		return OutcomeReadError, nil
	}
	ev.ScreenRead, ev.Screen = true, screen
	ev.Hint = detect.ClassifyScreen(screen, deps.Config.DetectionTailLines)
	ps.TurnUUID = ev.Turn.Entry.UUID

	switch ev.Hint {
	case detect.HintNativeRetrying:
		// 本体が request の中で待っている。Escape は待機ごと request を取り消す。
		standDown(ps)
		return OutcomeNativeAutoContinue, nil
	case detect.HintNativeArmed:
		if deps.Config.DeferToNativeAutoContinue {
			// ネイティブがカウントダウン中。Escape が取り消しキーなので、送ると継続そのものを潰す。
			standDown(ps)
			return OutcomeNativeAutoContinue, nil
		}
	case detect.HintLowerPriority:
		standDown(ps)
		return OutcomeLowerPriority, nil
	}

	if ev.Kind == detect.KindTransient && ev.Hint != detect.HintResumeReady {
		ps.Attempts++
		ps.WaitUntil = now.Add(transientBackoff(ps.Attempts, deps.Config))
		if err := recoverPane(ctx, deps, pane); err != nil {
			return OutcomeSendError, fmt.Errorf("pane %s への再開送信に失敗: %w", pane.PaneID, err)
		}
		return OutcomeRetried, nil
	}

	if ps.Attempts >= deps.Config.MaxRetries {
		ps.WaitUntil = now.Add(time.Duration(deps.Config.PollIntervalSeconds*maxRetriesCooldownMultiplier) * time.Second)
		return OutcomeMaxRetries, nil
	}

	ps.Attempts++
	ps.WaitUntil = now.Add(retryWaitAfterSend)

	if ev.Hint == detect.HintResumeReady {
		// Claude Code は Enter だけを待っている (上限の種類によらず)。テキストを打つと
		// 中断された turn の続きではなく新しい依頼として走ってしまう。
		if err := deps.Client.SendKeys(ctx, pane.PaneID, "enter"); err != nil {
			return OutcomeSendError, fmt.Errorf("pane %s への Enter 送信に失敗: %w", pane.PaneID, err)
		}
		return OutcomeResumeEnter, nil
	}

	if err := recoverPane(ctx, deps, pane); err != nil {
		return OutcomeSendError, fmt.Errorf("pane %s への再開送信に失敗: %w", pane.PaneID, err)
	}
	return OutcomeRetried, nil
}

// ComputeLimitWake は上限検知時の起床時刻を優先順位に従って計算する。
//  1. transcript の entry の quotaLimits.resetsAt が未来 → その時刻 + margin。過去なら now + margin
//     (解除時刻を過ぎても Claude Code は上限の画面のままキー入力を待つ)
//  2. その pane のセッションの statusline の state (own) の resets_at が未来 → その時刻 + margin
//  3. アカウント全体で最も新しいウィンドウ (account) の resets_at が未来 → その時刻 + margin
//  4. own / account の resets_at が過去 → now + margin (5 時間ウィンドウは既に切り替わっている)
//  5. どれも無い → FallbackWaitHours + margin
//
// 1 が本命。2.1.234 以降の rate_limit の entry は必ず resetsAt を持つ (実測 159 件)。2 以降は quotaLimits の無い
// 古い形の entry のための予備で、statusline の state は 429 で弾かれた turn では書かれないため、
// 上限中に開いた新規セッションでは own が無く account に頼る。2 と 4 に鮮度を要求しないのは、resets_at が
// 絶対時刻で使用率のように腐らないため。
func ComputeLimitWake(cfg config.Config, entry *transcript.Entry, rateState, account *ratelimit.State, now time.Time) (time.Time, WakeSource) {
	margin := time.Duration(cfg.MarginSeconds) * time.Second

	if entry != nil && entry.Quota != nil && !entry.Quota.ResetsAt.IsZero() {
		if entry.Quota.ResetsAt.After(now) {
			return entry.Quota.ResetsAt.Add(margin), WakeFromTranscript
		}
		return now.Add(margin), WakeTranscriptAlreadyReset
	}

	ownHas := rateState != nil && rateState.HasFiveHour()
	if ownHas && rateState.FiveHourResetsAt.After(now) {
		return rateState.FiveHourResetsAt.Add(margin), WakeFromStatusline
	}
	accountHas := account != nil && account.HasFiveHour()
	if accountHas && account.FiveHourResetsAt.After(now) {
		return account.FiveHourResetsAt.Add(margin), WakeFromAccount
	}
	if ownHas || accountHas {
		return now.Add(margin), WakeAlreadyReset
	}

	fallback := time.Duration(cfg.FallbackWaitHours * float64(time.Hour))
	return now.Add(fallback).Add(margin), WakeFromFallback
}

// WakeSource は起床時刻をどの情報源から決めたかを表す。inspect が判断過程を
// 説明するために使う。診断の説明と実際の計算がずれないよう、計算は
// ComputeLimitWake の 1 箇所だけに置き、inspect もそれを呼ぶ。
type WakeSource int

const (
	// WakeFromTranscript は transcript の entry の resetsAt (未来) を採用したことを表す。
	WakeFromTranscript WakeSource = iota
	// WakeTranscriptAlreadyReset は transcript の entry の resetsAt が既に過ぎており、待たずに起こすことを表す。
	WakeTranscriptAlreadyReset
	// WakeFromStatusline は自セッションの statusline の state の resets_at (未来) を採用したことを表す。
	WakeFromStatusline
	// WakeFromAccount はアカウント全体で最も新しいウィンドウの resets_at (未来) を採用したことを表す。
	WakeFromAccount
	// WakeAlreadyReset は statusline の state の resets_at が既に過ぎており、待たずに起こすことを表す。
	WakeAlreadyReset
	// WakeFromFallback は情報源が無く FallbackWaitHours に頼ったことを表す。
	WakeFromFallback
)

// String は WakeSource を診断表示向けの日本語にする。
func (s WakeSource) String() string {
	switch s {
	case WakeFromTranscript:
		return "transcript の entry の resetsAt (未来)"
	case WakeTranscriptAlreadyReset:
		return "transcript の entry の resetsAt (既に経過。解除済みとみなして即起床)"
	case WakeFromStatusline:
		return "自セッションの statusline の resets_at (未来)"
	case WakeFromAccount:
		return "他セッションの statusline の resets_at (アカウント全体で最新のウィンドウ、未来)"
	case WakeAlreadyReset:
		return "statusline の resets_at (既に経過。解除済みとみなして即起床)"
	case WakeFromFallback:
		return "フォールバック (transcript にも statusline にも解除時刻が無い)"
	default:
		return "不明"
	}
}

// wakeSourceIDs は WakeSource の JSON 向けの識別子。
var wakeSourceIDs = map[WakeSource]string{
	WakeFromTranscript:         "transcript",
	WakeTranscriptAlreadyReset: "transcript-already-reset",
	WakeFromStatusline:         "statusline",
	WakeFromAccount:            "account",
	WakeAlreadyReset:           "already-reset",
	WakeFromFallback:           "fallback",
}

// ID は WakeSource を JSON 向けの識別子にする (`inspect --json`)。String は日本語の説明。
func (s WakeSource) ID() string {
	if id, ok := wakeSourceIDs[s]; ok {
		return id
	}
	return "unknown"
}

// transientBackoff は transient エラーの指数バックオフを計算する
// (base * 2^attempts を TransientMaxWaitSeconds で cap する)。
// cap に届いた時点で倍にするのをやめる。先に 2^attempts を掛けると、transient の再試行は回数に上限が無いので
// 既定の 60 秒でも 28 回目で int64 を溢れて負になり、待たずに送り続ける (invariants_test.go が見つけた)。
func transientBackoff(attempts int, cfg config.Config) time.Duration {
	base := time.Duration(cfg.TransientWaitSeconds) * time.Second
	capped := time.Duration(cfg.TransientMaxWaitSeconds) * time.Second

	backoff := base
	for range attempts {
		if backoff >= capped {
			break
		}
		backoff *= 2
	}
	return min(backoff, capped)
}

// recoverPane は再開の分割送信 (esc → 一時停止 → メッセージ送信 → 一時停止 → enter) を行う。
// Claude のペースト検知と /rate-limit-options メニューを避けるため、キー入力を分けている。
func recoverPane(ctx context.Context, deps Deps, pane multiplexer.Pane) error {
	if deps.Config.DismissMenu {
		if err := deps.Client.SendKeys(ctx, pane.PaneID, "esc"); err != nil {
			return err
		}
		deps.Sleep(time.Duration(deps.Config.MenuDismissDelayMs) * time.Millisecond)
	}

	if err := deps.Client.SendText(ctx, pane.PaneID, deps.Config.RetryMessage); err != nil {
		return err
	}
	deps.Sleep(time.Duration(deps.Config.SubmitDelayMs) * time.Millisecond)

	return deps.Client.SendKeys(ctx, pane.PaneID, "enter")
}
