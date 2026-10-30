// Package monitor は多重化ツールの pane 1 枚ぶんの状態機械を実装する。
//
// 「monitoring (通常監視)」と「waiting (上限解除 / 一時エラー待ち)」の 2 状態を持ち、
// 呼び出し元 (daemon) が pane ごとに *PaneState を保持して Tick を毎ポーリング呼び出す
// 想定である。このパッケージ自身は PaneList を呼ばない。pane の発見・列挙は
// daemon の責務であり、ここでは渡された 1 pane を判断するだけに徹する。
//
// 参考実装は https://github.com/mo-arvan/herdr-claude-auto-retry の
// src/monitor-core.js (processOneTick)。方式はそこから借りているが、以下の点は
// 意図的に変えている。
//
//   - 多重化ツールの agent_status を検知に使わない。参考実装は working な pane を対象外に
//     するが、上限で止まった pane は出力が止まらない pane と区別できず working と
//     報告され続けることがある (実測)。判定は画面テキストの分類だけで行う。agent_status を見るのは
//     待機明けの送信を止めるときだけ (利用者の答えを待つ prompt が開いていれば送らない)。
//   - statusline 由来の 5 時間ウィンドウ使用率によるゲート (fresh かつ閾値未満なら
//     limit 発火を見送る) は参考実装に無い、このツール独自の安全策。
//   - 遷移結果を文字列ではなく Outcome という列挙型で返す。
//   - Client の呼び出しがエラーを返しても状態機械を壊さず、型付きの Outcome と
//     error を両方返して呼び出し元 (daemon) が連続失敗をカウントできるようにする。
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
}

// Outcome は Tick が 1 tick ぶんに行った (あるいは行わなかった) ことを表す。
// daemon はこれをログに出すために使う。
type Outcome int

const (
	// OutcomeStillWaiting は待機中でまだ起床時刻に達していないため何もしなかったことを表す。
	OutcomeStillWaiting Outcome = iota
	// OutcomeScreenUnreadable は待機中に画面が空、または読み取りに失敗したため
	// 何もせず待機を継続したことを表す。ユーザーが resume したとは判断しない。
	OutcomeScreenUnreadable
	// OutcomeRecoveredByUser は待機中にユーザー自身が limit/transient から復帰したと
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
	// OutcomeMonitoring は監視中で異常が無かったことを表す。
	OutcomeMonitoring
	// OutcomeGateSuppressed は画面は limit に見えたが、fresh な statusline state が
	// 閾値未満だったため発火を見送ったことを表す。
	OutcomeGateSuppressed
	// OutcomeWaiting は監視中から待機状態へ入ったことを表す。
	OutcomeWaiting
	// OutcomeReadError は PaneRead がエラーを返したことを表す (監視中)。
	OutcomeReadError
	// OutcomeSendError は待機中の回復送信 (esc/送信/enter) の途中でエラーが
	// 発生したことを表す。状態遷移自体は行われている。
	OutcomeSendError
	// OutcomeNativeAutoContinue は Claude Code 自身の auto-continue が解除を
	// 待っているため、このツールが手を引いたことを表す。
	OutcomeNativeAutoContinue
	// OutcomeResumeEnter は上限が解除済みで Enter 待ちの pane へ Enter を
	// 送ったことを表す。
	OutcomeResumeEnter
	// OutcomeHardCap は待っても解除されない上限 (spend limit / usage credit) を
	// 検知し、待機に入らなかったことを表す。
	OutcomeHardCap
	// OutcomeBlockedByPrompt は待機明けに Claude Code が利用者の答えを待つ prompt (permission / question /
	// auth) を開いていたため、送らずに待機を延長したことを表す。
	OutcomeBlockedByPrompt
)

// outcomeNames は Outcome のログ向けの識別子。
var outcomeNames = map[Outcome]string{
	OutcomeStillWaiting:       "still-waiting",
	OutcomeScreenUnreadable:   "screen-unreadable",
	OutcomeRecoveredByUser:    "recovered-by-user",
	OutcomeNotClaudeAgent:     "not-claude-agent",
	OutcomeMaxRetries:         "max-retries",
	OutcomeRetried:            "retried",
	OutcomeMonitoring:         "monitoring",
	OutcomeGateSuppressed:     "gate-suppressed",
	OutcomeWaiting:            "waiting",
	OutcomeReadError:          "read-error",
	OutcomeSendError:          "send-error",
	OutcomeNativeAutoContinue: "native-auto-continue",
	OutcomeResumeEnter:        "resume-enter",
	OutcomeHardCap:            "hard-cap",
	OutcomeBlockedByPrompt:    "blocked-by-prompt",
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
	// notClaudeBackoffMultiplier は「pane が Claude agent でなくなった」ときの
	// 再チェック間隔を PollIntervalSeconds の何倍にするか。
	notClaudeBackoffMultiplier = 6
	// maxRetriesCooldownMultiplier は再試行上限到達後のクールダウンを
	// PollIntervalSeconds の何倍にするか。
	maxRetriesCooldownMultiplier = 12
	// retryWaitAfterSend は通常の (transient でない) 再送信後、次に画面を
	// 確認するまでの待ち時間。
	retryWaitAfterSend = 30 * time.Second
)

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

// Deps は状態遷移に必要な依存をまとめたもの。テストでは Client に
// multiplexerfake.Client を、Sleep に記録用のダミー関数を注入する。
type Deps struct {
	Client     Client
	Classifier *detect.Classifier
	Config     config.Config
	// Sleep は recover 中の一時停止に使う。本番では time.Sleep を渡す想定。
	// テストは実際に待たないよう no-op またはログ記録用の関数を渡す。
	Sleep func(time.Duration)
	// 以下は tick ごとに daemon が読み直して全 pane で共有する state。
	// Account はアカウント全体で最も新しい 5 時間ウィンドウの state
	// (ratelimit.Store.LatestWindow)。resets_at はアカウント共通なので、自分の state を
	// 持たない pane の起床時刻をここから引く。Config と同じく tick ごとに daemon が
	// 読み直して全 pane で共有する。無ければ nil。
	Account *ratelimit.State
	// Observe は画面を読んで分類するたびに、分類した画面そのものと kind を受け取る (nil なら呼ばない)。
	// daemon が上限の出来事の記録 (daemon/limitlog) に「判定した画面」を添えるための口で、判定には関わらない。
	// 後から読み直すとき、数秒後に読み直した画面は同じ証拠にならないのでここで渡す。
	Observe func(pane multiplexer.Pane, kind detect.Kind, screen string)
}

// Tick は pane 1 枚ぶんの 1 tick を処理し、ps を必要に応じて書き換える。
//
// rateState は statusline.sh が書く、この pane のセッションぶんの利用上限 state。
// セッションごとに別ファイルなので daemon が pane ごとに読み込んで渡す。
// 対応する state が無い場合は nil を渡してよい。
//
// Client の呼び出しがエラーを返しても panic や状態破壊はしない。エラーは
// Outcome と共に返すので、呼び出し元は連続失敗のカウント等に使うこと。
func Tick(ctx context.Context, deps Deps, pane multiplexer.Pane, ps *PaneState, rateState *ratelimit.State, now time.Time) (Outcome, error) {
	if ps.Status == StatusWaiting && now.Before(ps.WaitUntil) {
		return OutcomeStillWaiting, nil
	}

	screen, err := deps.Client.PaneRead(ctx, pane.PaneID, multiplexer.PaneReadOptions{Lines: deps.Config.ReadLines})
	if err != nil {
		if ps.Status == StatusWaiting {
			// 読み取り失敗をユーザーの resume と取り違えない。待機はそのまま継続する。
			return OutcomeScreenUnreadable, fmt.Errorf("pane %s の読み取りに失敗: %w", pane.PaneID, err)
		}
		return OutcomeReadError, fmt.Errorf("pane %s の読み取りに失敗: %w", pane.PaneID, err)
	}

	if ps.Status == StatusWaiting && strings.TrimSpace(screen) == "" {
		// 空画面はユーザーが resume した証拠にならない。待機を継続する。
		return OutcomeScreenUnreadable, nil
	}

	kind := deps.Classifier.Classify(screen, deps.Config.DetectionTailLines)
	if kind == detect.KindAutoContinueArmed && !deps.Config.DeferToNativeAutoContinue {
		// ネイティブへ譲る設定を切ったときは、同じ画面を通常の上限として扱う。
		kind = detect.KindLimit
	}
	if deps.Observe != nil {
		deps.Observe(pane, kind, screen)
	}

	if outcome, handled, err := tickNative(ps, kind); handled {
		return outcome, err
	}

	limited := kind == detect.KindLimit ||
		(kind == detect.KindTransient && deps.Config.HandleTransient) ||
		kind == detect.KindResumeReady

	if ps.Status == StatusWaiting {
		// 待機明けにはゲートを掛けない。解除時刻を過ぎると 5 時間ウィンドウは
		// 切り替わって使用率は下がるが、Claude Code は上限の画面のままキー入力を
		// 待つ。ここで fresh な低使用率を理由に見送ると「ユーザー自身が復帰した」と
		// 取り違えて監視へ戻り、state が古くなってから改めて検知して次のウィンドウの
		// resets_at (5 時間先) まで寝てしまう。ゲートは待機に入るときの誤検知よけであって、
		// 待機明けの送信を止めるものではない。
		return tickWaiting(ctx, deps, pane, ps, kind, limited, now)
	}

	gateSuppressed := kind == detect.KindLimit && LimitGate(deps.Config, rateState, now).suppresses()
	return tickMonitoring(deps, ps, kind, limited && !gateSuppressed, gateSuppressed, rateState, screen, now)
}

// tickNative は「このツールが待機に入ってはいけない」画面を処理する。
// handled=true を返したら、通常の待機/再開の経路には進まない。
//
// Enter 待ち (KindResumeReady) はここでは扱わない。あれは待機して送る状態なので、
// 通常の状態機械 (待機 → 送信 → maxRetries で打ち切り) にそのまま乗せる。
func tickNative(ps *PaneState, kind detect.Kind) (Outcome, bool, error) {
	switch kind {
	case detect.KindAutoContinueArmed:
		// ネイティブがカウントダウン中。この状態では Escape が取り消しキーなので
		// (画面に "esc or type to cancel" と出ている)、待機明けに送ると継続そのものを
		// 潰す。ネイティブが降りた画面は別の文言になり、そのとき改めて拾える。
		// (この分岐へ来るのは deferToNativeAutoContinue が有効なときだけ。
		// 無効なら呼び出し元が KindLimit へ落としてある。)
		standDown(ps)
		return OutcomeNativeAutoContinue, true, nil

	case detect.KindSpendLimit:
		// 待っても解除されない上限。待機に入ると解除時刻に送信 → 再び弾かれる、を
		// maxRetries まで繰り返すだけになる。
		standDown(ps)
		return OutcomeHardCap, true, nil

	default:
		return 0, false, nil
	}
}

// Claude Code が blocked の record に付ける kind のうち、利用者が答える prompt が開いているもの。
// 限度の画面がこの kind で来ることは無いので、列挙に無い kind (dialog 一般の kind 無し) は送信を止めない
// (上限到達時の record は未実測。#77)。
// cc-premise: 2.1.302 osc7501 blocked の kind は permission / question / auth。dialog 一般は kind 無し
const (
	kindPermission = "permission"
	kindQuestion   = "question"
	kindAuth       = "auth"
)

// waitingOnPrompt は pane が利用者の答えを待つ prompt で止まっているかを返す。
// この pane へ esc を送ると prompt を断り、Enter を送ると承認する。
func waitingOnPrompt(pane multiplexer.Pane) bool {
	if pane.AgentStatus != multiplexer.AgentStatusBlocked {
		return false
	}
	switch pane.AgentKind {
	case kindPermission, kindQuestion, kindAuth:
		return true
	}
	return false
}

// standDown は待機中だった pane を監視状態へ戻す。
func standDown(ps *PaneState) {
	if ps.Status != StatusWaiting {
		return
	}
	ps.Status = StatusMonitoring
	ps.Attempts = 0
}

// Gate は limit 検知に対するゲートの判定結果と、その根拠を表す。inspect が
// 判断過程を説明するために使う。WakeSource と同じ理由で、判定は LimitGate の
// 1 箇所だけに置き、inspect もそれを呼ぶ (説明と実際の挙動がずれないように)。
type Gate int

const (
	// GateNoState は state ファイルが無いことを表す。
	GateNoState Gate = iota
	// GateNoFiveHour は five_hour が null で書かれていることを表す。
	GateNoFiveHour
	// GateStale は観測が古いことを表す。
	GateStale
	// GateAboveThreshold は fresh な観測が使用率閾値以上であることを表す。
	GateAboveThreshold
	// GateBelowThreshold は fresh な観測が使用率閾値未満であることを表す。
	// ゲートが働くのはこの場合だけ。
	GateBelowThreshold
)

// suppresses は limit 検知を抑制するかを返す。判定は Tick の中だけで使い、inspect は
// String の説明文を出す。
func (g Gate) suppresses() bool {
	return g == GateBelowThreshold
}

// String は Gate を診断表示向けの日本語にする。
func (g Gate) String() string {
	switch g {
	case GateNoState:
		return "state ファイルが無いため、ゲートは働きません (画面判定をそのまま通します)"
	case GateNoFiveHour:
		return "state の five_hour が null (ウィンドウ切り替わり瞬間の描画) のため、ゲートは働きません (画面判定をそのまま通します)"
	case GateStale:
		return "state は古いため、ゲートは働きません (画面判定をそのまま通します)"
	case GateAboveThreshold:
		return "閾値以上: limit 検知時にゲートで抑制しません (発火を通します)"
	case GateBelowThreshold:
		return "閾値未満: limit と判定されても fresh な間はゲートで抑制されます (送信しません)"
	default:
		return "不明"
	}
}

// LimitGate は statusline state による limit 検知のゲートを判定する。
// 抑制するのは fresh な観測が使用率閾値未満のときだけで、state が無い、
// five_hour が null で書かれている、または古い場合は判断材料が無いので
// 画面テキストだけによる検知をそのまま通す。
func LimitGate(cfg config.Config, rateState *ratelimit.State, now time.Time) Gate {
	if rateState == nil {
		return GateNoState
	}
	if !rateState.HasFiveHour() {
		return GateNoFiveHour
	}
	maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
	if !rateState.IsFresh(now, maxAge) {
		return GateStale
	}
	if rateState.FiveHourExhausted(cfg.UsedPercentageThreshold) {
		return GateAboveThreshold
	}
	return GateBelowThreshold
}

// tickWaiting は待機状態での 1 tick を処理する。
func tickWaiting(ctx context.Context, deps Deps, pane multiplexer.Pane, ps *PaneState, kind detect.Kind, limited bool, now time.Time) (Outcome, error) {
	if !limited {
		ps.Status = StatusMonitoring
		ps.Attempts = 0
		return OutcomeRecoveredByUser, nil
	}

	if !multiplexer.IsClaudeAgent(pane) {
		ps.WaitUntil = now.Add(time.Duration(deps.Config.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
		return OutcomeNotClaudeAgent, nil
	}

	// 画面に上限の文言が残ったまま permission prompt などが開いていることがある。esc は prompt を断り、
	// Enter は承認するので、利用者が答えるまで送らない。agent_status を見るのはこの送信の抑止だけで、
	// 検知には使わない (上限で止まった pane は working と申告され続けることがある)。
	if waitingOnPrompt(pane) {
		ps.WaitUntil = now.Add(time.Duration(deps.Config.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
		return OutcomeBlockedByPrompt, nil
	}

	if kind == detect.KindTransient {
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

	if kind == detect.KindResumeReady {
		// 上限は解除済みで、Claude Code は Enter だけを待っている。テキストを打つと
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

// tickMonitoring は監視状態での 1 tick を処理する。上限でも一時エラーでもない画面には何もしない。
func tickMonitoring(deps Deps, ps *PaneState, kind detect.Kind, limited bool, gateSuppressed bool, rateState *ratelimit.State, screen string, now time.Time) (Outcome, error) {
	if gateSuppressed {
		return OutcomeGateSuppressed, nil
	}
	if !limited {
		return OutcomeMonitoring, nil
	}

	ps.Status = StatusWaiting
	switch kind {
	case detect.KindTransient:
		ps.WaitUntil = now.Add(time.Duration(deps.Config.TransientWaitSeconds) * time.Second)
	case detect.KindResumeReady:
		// 上限は既に解除済みで、Claude Code はキー入力だけを待っている。待つ理由が
		// 無いので次の tick で送る。
		ps.WaitUntil = now
	default:
		ps.WaitUntil, _ = ComputeLimitWake(deps.Config, rateState, deps.Account, screen, now)
	}
	return OutcomeWaiting, nil
}

// ComputeLimitWake は上限検知時の起床時刻を優先順位に従って計算する。
//  1. その pane のセッションの state (own) の resets_at が未来 → その時刻 + margin
//  2. 画面の絶対時刻 (`resets 8:10am (Asia/Tokyo)`) が読める → 未来ならそれ + margin、
//     過去なら now + margin
//  3. アカウント全体で最も新しいウィンドウ (account) の resets_at が未来 → その時刻 + margin
//  4. own / account の resets_at が過去 → now + margin (5 時間ウィンドウは既に切り替わっている)
//  5. 画面の相対表現 (「12 分後」等) が読み取れる → それ + margin
//  6. どれも無い → FallbackWaitHours + margin
//
// 1 と 4 に鮮度 (IsFresh) を要求しない。resets_at は絶対時刻で、使用率のように時間で
// 腐る値ではない。むしろ上限で止まったセッションは statusline を再描画しないため、
// state は「一番必要なタイミングで必ず古い」。鮮度を要求すると、正確な解除時刻を
// 持っているのに捨てて後段へ後退してしまう。
//
// 2 と 3 が要るのは、own state を持たない pane があるため。上限中に開いた新規セッションは
// statusline がまだ何も書いておらず、429 で弾かれた turn も state を書かない。own だけを
// 見ると 6 (5 時間待ち) に落ちる (2026-08-28 07:35 に実測)。resets_at はアカウント全体で
// 共通なので他セッションの state から引ける (3)。画面の時刻はその pane が止まった
// ウィンドウを直接示す (2)。
//
// 2 を 3 より上に置くのは、解除後に上限の画面のまま止まっている pane (ネイティブの
// auto-continue が取り消された等) のため。他セッションが次のウィンドウで動き出すと
// account は「次のウィンドウの終わり」を指すが、画面の時刻は過去のままなので今すぐ
// 突ける。逆に own が過去で画面が未来のときは、own が前ウィンドウの残骸で、pane は
// 現在のウィンドウで弾かれている (2026-08-28 07:34 の誤射がこれ)。
//
// 4 が要るのは、解除時刻を過ぎても画面が上限表示のまま止まっていることがあるため
// (Claude Code はキー入力を待つ)。ここで 6 へ落ちると、待つ理由が無いのに
// FallbackWaitHours ぶん寝てしまう。
//
// 見ているのは 5 時間ウィンドウだけ。週次上限で止まっている pane はこの計算では
// 正しく扱えず、送信 → 再度上限 → 再試行を maxRetries まで繰り返してクールダウンに入る。
func ComputeLimitWake(cfg config.Config, rateState, account *ratelimit.State, screen string, now time.Time) (time.Time, WakeSource) {
	return computeLimitWakeIn(cfg, rateState, account, screen, now, time.Local)
}

// computeLimitWakeIn は ComputeLimitWake の本体。画面の絶対時刻に tz 表記が無いときの
// 解釈に使う local を差し替えられるようにしてある (テスト用)。
func computeLimitWakeIn(cfg config.Config, rateState, account *ratelimit.State, screen string, now time.Time, local *time.Location) (time.Time, WakeSource) {
	margin := time.Duration(cfg.MarginSeconds) * time.Second

	ownHas := rateState != nil && rateState.HasFiveHour()
	if ownHas {
		if resetsAt := rateState.FiveHourResetsAt; resetsAt.After(now) {
			return resetsAt.Add(margin), WakeFromResetsAt
		}
	}

	if at, ok := detect.ParseAbsoluteReset(screen, cfg.DetectionTailLines, now, local); ok {
		if at.After(now) {
			return at.Add(margin), WakeFromScreenAbsolute
		}
		return now.Add(margin), WakeScreenAlreadyReset
	}

	accountHas := account != nil && account.HasFiveHour()
	if accountHas {
		if resetsAt := account.FiveHourResetsAt; resetsAt.After(now) {
			return resetsAt.Add(margin), WakeFromAccount
		}
	}

	if ownHas || accountHas {
		return now.Add(margin), WakeAlreadyReset
	}

	if wait, ok := detect.ParseRelativeWait(screen, cfg.DetectionTailLines); ok {
		return now.Add(wait).Add(margin), WakeFromRelative
	}

	fallback := time.Duration(cfg.FallbackWaitHours * float64(time.Hour))
	return now.Add(fallback).Add(margin), WakeFromFallback
}

// WakeSource は起床時刻をどの情報源から決めたかを表す。inspect が判断過程を
// 説明するために使う。診断の説明と実際の計算がずれないよう、計算は
// ComputeLimitWake の 1 箇所だけに置き、inspect もそれを呼ぶ。
type WakeSource int

const (
	// WakeFromResetsAt は自セッションの state の resets_at (未来) を採用したことを表す。
	WakeFromResetsAt WakeSource = iota
	// WakeAlreadyReset は自セッションまたはアカウント全体の state の resets_at が既に
	// 過ぎており、待たずに起こすことを表す。
	WakeAlreadyReset
	// WakeFromScreenAbsolute は画面の絶対時刻 (未来) を採用したことを表す。
	WakeFromScreenAbsolute
	// WakeScreenAlreadyReset は画面の絶対時刻が既に過ぎており、待たずに起こすことを表す。
	WakeScreenAlreadyReset
	// WakeFromAccount はアカウント全体で最も新しいウィンドウの resets_at (未来) を
	// 採用したことを表す (自セッションの state が無い / 古い)。
	WakeFromAccount
	// WakeFromRelative は画面の相対表現を採用したことを表す。
	WakeFromRelative
	// WakeFromFallback は情報源が無く FallbackWaitHours に頼ったことを表す。
	WakeFromFallback
)

// String は WakeSource を診断表示向けの日本語にする。
func (s WakeSource) String() string {
	switch s {
	case WakeFromResetsAt:
		return "自セッションの statusline の resets_at (未来)"
	case WakeAlreadyReset:
		return "statusline の resets_at (既に経過。解除済みとみなして即起床)"
	case WakeFromScreenAbsolute:
		return "画面の絶対時刻 (未来)"
	case WakeScreenAlreadyReset:
		return "画面の絶対時刻 (既に経過。解除済みとみなして即起床)"
	case WakeFromAccount:
		return "他セッションの statusline の resets_at (アカウント全体で最新のウィンドウ、未来)"
	case WakeFromRelative:
		return "画面の相対時刻表現"
	case WakeFromFallback:
		return "フォールバック (state も画面の相対表現も無い)"
	default:
		return "不明"
	}
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
