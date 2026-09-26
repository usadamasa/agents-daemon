// Package monitor は herdr pane 1 枚ぶんの状態機械を実装する。
//
// 「monitoring (通常監視)」と「waiting (上限解除 / 一時エラー待ち)」の 2 状態を持ち、
// 呼び出し元 (daemon) が pane ごとに *PaneState を保持して Tick を毎ポーリング呼び出す
// 想定である。このパッケージ自身は herdr pane list を叩かない。pane の発見・列挙は
// daemon の責務であり、ここでは渡された 1 pane を判断するだけに徹する。
//
// 参考実装は https://github.com/mo-arvan/herdr-claude-auto-retry の
// src/monitor-core.js (processOneTick)。方式はそこから借りているが、以下の点は
// 意図的に変えている。
//
//   - herdr の agent_status を判定に使わない。参考実装は working な pane を対象外に
//     するが、上限で止まった pane は出力が止まらない pane と区別できず working と
//     報告され続けることがある (実測)。判定は画面テキストの分類だけで行う。
//   - statusline 由来の 5 時間ウィンドウ使用率によるゲート (fresh かつ閾値未満なら
//     limit 発火を見送る) は参考実装に無い、このツール独自の安全策。
//   - 遷移結果を文字列ではなく Outcome という列挙型で返す。
//   - herdrcli の呼び出しがエラーを返しても状態機械を壊さず、型付きの Outcome と
//     error を両方返して呼び出し元 (daemon) が連続失敗をカウントできるようにする。
package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
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

	// CompactSignature は compact 停止の候補として観測している画面の署名
	// (compact 境界行から空の入力行まで)。空文字列は「候補ではない」。
	CompactSignature string
	// CompactSeenAt は CompactSignature を最初に観測した時刻。ここから
	// CompactStallQuietSeconds のあいだ変わらなければ「止まっている」と見なす。
	CompactSeenAt time.Time
	// CompactNudgedAt は直近で compact 停止の継続要求を送った時刻。
	CompactNudgedAt time.Time
	// CompactNudges は今の停止に対して継続要求を送った回数。停止が解けたら 0 に戻る。
	CompactNudges int

	// CompactPrepSentAt は /compact-prep を投入した時刻。非ゼロは
	// 「state file が書かれるのを待っている」を意味する。
	CompactPrepSentAt time.Time
	// CompactAutoSentAt は自動 compact の段が直近で何かを送った (あるいは
	// 時間切れで諦めた) 時刻。1 段目のクールダウンの起点。
	CompactAutoSentAt time.Time
	// CompactResumedAt は圧縮完了 marker を見て再開を送った時刻。
	CompactResumedAt time.Time
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
	// OutcomeCompactNudged は compact 直後に止まった pane へ継続を促す
	// メッセージを送ったことを表す (画面判定による fallback 経路)。
	OutcomeCompactNudged
	// OutcomeCompactNudgeCapped は同じ停止への継続要求が上限回数に達したため
	// 送らなかったことを表す。詰まった run は人が見る。
	OutcomeCompactNudgeCapped
	// OutcomeCompactPrepSent は context 使用率が閾値を超えた pane へ
	// /compact-prep を投入したことを表す。
	OutcomeCompactPrepSent
	// OutcomeCompactSent は compact-prep の state file を確認して /compact を
	// 投入したことを表す。
	OutcomeCompactSent
	// OutcomeCompactPrepTimeout は /compact-prep を投入したが state file が
	// 現れないまま時間切れになったことを表す。
	OutcomeCompactPrepTimeout
	// OutcomeCompactResumed は圧縮完了 marker を見て作業の再開を促す
	// メッセージを送ったことを表す。
	OutcomeCompactResumed
)

// String は Outcome をログ向けの識別子文字列にする。
func (o Outcome) String() string {
	switch o {
	case OutcomeStillWaiting:
		return "still-waiting"
	case OutcomeScreenUnreadable:
		return "screen-unreadable"
	case OutcomeRecoveredByUser:
		return "recovered-by-user"
	case OutcomeNotClaudeAgent:
		return "not-claude-agent"
	case OutcomeMaxRetries:
		return "max-retries"
	case OutcomeRetried:
		return "retried"
	case OutcomeMonitoring:
		return "monitoring"
	case OutcomeGateSuppressed:
		return "gate-suppressed"
	case OutcomeWaiting:
		return "waiting"
	case OutcomeReadError:
		return "read-error"
	case OutcomeSendError:
		return "send-error"
	case OutcomeNativeAutoContinue:
		return "native-auto-continue"
	case OutcomeResumeEnter:
		return "resume-enter"
	case OutcomeHardCap:
		return "hard-cap"
	case OutcomeCompactNudged:
		return "compact-nudged"
	case OutcomeCompactNudgeCapped:
		return "compact-nudge-capped"
	case OutcomeCompactPrepSent:
		return "compact-prep-sent"
	case OutcomeCompactSent:
		return "compact-sent"
	case OutcomeCompactPrepTimeout:
		return "compact-prep-timeout"
	case OutcomeCompactResumed:
		return "compact-resumed"
	default:
		return "unknown"
	}
}

// metadataSource は herdr pane report-metadata --source に渡す固定値。
const metadataSource = "agents-daemon"

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
	// engagedLabelTTLPollMultiplier は待機ラベルの TTL を PollIntervalSeconds の
	// 何倍にするか。daemon が異常終了して更新が止まっても、この時間が過ぎれば
	// herdr 側でラベルが自動的に消える。
	engagedLabelTTLPollMultiplier = 10
)

// Deps は状態遷移に必要な依存をまとめたもの。テストでは Client に
// herdrclifake.Client を、Sleep に記録用のダミー関数を注入する。
type Deps struct {
	Client     herdrcli.Client
	Classifier *detect.Classifier
	Config     config.Config
	// Sleep は recover 中の一時停止に使う。本番では time.Sleep を渡す想定。
	// テストは実際に待たないよう no-op またはログ記録用の関数を渡す。
	Sleep func(time.Duration)
	// Account はアカウント全体で最も新しい 5 時間ウィンドウの state
	// (sessionstate.Store.LatestWindow)。resets_at はアカウント共通なので、自分の state を
	// 持たない pane の起床時刻をここから引く。Config と同じく tick ごとに daemon が
	// 読み直して全 pane で共有する。無ければ nil。
	Account *sessionstate.RateLimit
	// CompactState は pane のセッションに対応する compact 関連の state
	// (context 使用率 / compact-prep の state file / 圧縮完了 marker) を返す。
	// daemon が実装を差し、読み込みエラーは daemon 側でログに出して nil を返す。
	//
	// rateState のように引数で渡さないのは、compact の 3 段はいずれも監視状態の
	// 最後まで到達しないと使わないため。到達しない tick でファイルを読まずに済む。
	// このフィールド自体が nil なら compact の自動化は適用しない。
	CompactState func(sessionID string) *sessionstate.Compact
}

// Tick は pane 1 枚ぶんの 1 tick を処理し、ps を必要に応じて書き換える。
//
// rateState は statusline.sh が書く、この pane のセッションぶんの利用上限 state。
// セッションごとに別ファイルなので daemon が pane ごとに読み込んで渡す。
// 対応する state が無い場合は nil を渡してよい。
//
// herdrcli の呼び出しがエラーを返しても panic や状態破壊はしない。エラーは
// Outcome と共に返すので、呼び出し元は連続失敗のカウント等に使うこと。
func Tick(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState, rateState *sessionstate.RateLimit, now time.Time) (Outcome, error) {
	if ps.Status == StatusWaiting && now.Before(ps.WaitUntil) {
		return OutcomeStillWaiting, nil
	}

	screen, err := deps.Client.PaneRead(ctx, pane.PaneID, herdrcli.PaneReadOptions{
		Source: herdrcli.PaneReadSource(deps.Config.ReadSource),
		Lines:  deps.Config.ReadLines,
	})
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

	if outcome, handled, err := tickNative(ctx, deps, pane, ps, kind); handled {
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

	gateSuppressed := kind == detect.KindLimit && LimitGate(deps.Config, rateState, now).Suppresses()
	return tickMonitoring(ctx, deps, pane, ps, kind, limited && !gateSuppressed, gateSuppressed, rateState, screen, now)
}

// tickNative は「このツールが待機に入ってはいけない」画面を処理する。
// handled=true を返したら、通常の待機/再開の経路には進まない。
//
// Enter 待ち (KindResumeReady) はここでは扱わない。あれは待機して送る状態なので、
// 通常の状態機械 (待機 → 送信 → maxRetries で打ち切り) にそのまま乗せる。
func tickNative(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState, kind detect.Kind) (Outcome, bool, error) {
	switch kind {
	case detect.KindAutoContinueArmed:
		// ネイティブがカウントダウン中。この状態では Escape が取り消しキーなので
		// (画面に "esc or type to cancel" と出ている)、待機明けに送ると継続そのものを
		// 潰す。ネイティブが降りた画面は別の文言になり、そのとき改めて拾える。
		// (この分岐へ来るのは deferToNativeAutoContinue が有効なときだけ。
		// 無効なら呼び出し元が KindLimit へ落としてある。)
		standDown(ctx, deps, pane, ps)
		return OutcomeNativeAutoContinue, true, nil

	case detect.KindSpendLimit:
		// 待っても解除されない上限。待機に入ると解除時刻に送信 → 再び弾かれる、を
		// maxRetries まで繰り返すだけになる。
		standDown(ctx, deps, pane, ps)
		return OutcomeHardCap, true, nil

	default:
		return 0, false, nil
	}
}

// standDown は待機中だった pane を監視状態へ戻す。ラベル解除の失敗で状態遷移
// そのものを止めない (ベストエフォート)。
func standDown(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState) {
	if ps.Status != StatusWaiting {
		return
	}
	ps.Status = StatusMonitoring
	ps.Attempts = 0
	_ = clearEngagedLabel(ctx, deps, pane)
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

// Suppresses は limit 検知を抑制するかを返す。
func (g Gate) Suppresses() bool {
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
func LimitGate(cfg config.Config, rateState *sessionstate.RateLimit, now time.Time) Gate {
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
func tickWaiting(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState, kind detect.Kind, limited bool, now time.Time) (Outcome, error) {
	if !limited {
		ps.Status = StatusMonitoring
		ps.Attempts = 0
		// ラベル解除の失敗で状態遷移そのものを止めない。ベストエフォートで扱う。
		_ = clearEngagedLabel(ctx, deps, pane)
		return OutcomeRecoveredByUser, nil
	}

	if !herdrcli.IsClaudeAgent(pane) {
		ps.WaitUntil = now.Add(time.Duration(deps.Config.PollIntervalSeconds*notClaudeBackoffMultiplier) * time.Second)
		return OutcomeNotClaudeAgent, nil
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

// tickMonitoring は監視状態での 1 tick を処理する。
func tickMonitoring(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState, kind detect.Kind, limited bool, gateSuppressed bool, rateState *sessionstate.RateLimit, screen string, now time.Time) (Outcome, error) {
	if gateSuppressed {
		return OutcomeGateSuppressed, nil
	}
	if !limited {
		return tickCompact(ctx, deps, pane, ps, screen, now)
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

	if err := publishEngagedLabel(ctx, deps, pane); err != nil {
		return OutcomeWaiting, fmt.Errorf("pane %s へのラベル表示に失敗: %w", pane.PaneID, err)
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
func ComputeLimitWake(cfg config.Config, rateState, account *sessionstate.RateLimit, screen string, now time.Time) (time.Time, WakeSource) {
	return computeLimitWakeIn(cfg, rateState, account, screen, now, time.Local)
}

// computeLimitWakeIn は ComputeLimitWake の本体。画面の絶対時刻に tz 表記が無いときの
// 解釈に使う local を差し替えられるようにしてある (テスト用)。
func computeLimitWakeIn(cfg config.Config, rateState, account *sessionstate.RateLimit, screen string, now time.Time, local *time.Location) (time.Time, WakeSource) {
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
func transientBackoff(attempts int, cfg config.Config) time.Duration {
	base := time.Duration(cfg.TransientWaitSeconds) * time.Second
	capped := time.Duration(cfg.TransientMaxWaitSeconds) * time.Second

	// シフト量を抑えて time.Duration (int64) のオーバーフローを避ける。
	// 実運用では cap が先に効くのでこの上限で十分。
	shift := min(attempts, 32)
	backoff := base * time.Duration(int64(1)<<uint(shift)) // #nosec G115 -- shift は 32 以下に抑制済み

	return min(backoff, capped)
}

// recoverPane は再開の分割送信 (esc → 一時停止 → メッセージ送信 → 一時停止 → enter) を行う。
// Claude のペースト検知と /rate-limit-options メニューを避けるため、キー入力を分けている。
func recoverPane(ctx context.Context, deps Deps, pane herdrcli.Pane) error {
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

// publishEngagedLabel と clearEngagedLabel は待機ラベルの表示/解除だけを担う。
// 状態遷移の判断ロジック (tickWaiting/tickMonitoring) から分離してあるので、
// ラベル表示の失敗が遷移テストの assertion に混ざらない。

func publishEngagedLabel(ctx context.Context, deps Deps, pane herdrcli.Pane) error {
	return deps.Client.ReportMetadata(ctx, pane.PaneID, herdrcli.ReportMetadataOptions{
		Source: metadataSource,
		StateLabels: map[herdrcli.AgentStatus]string{
			pane.AgentStatus: deps.Config.EngagedLabel,
		},
		TTLMillis: deps.Config.PollIntervalSeconds * 1000 * engagedLabelTTLPollMultiplier,
	})
}

func clearEngagedLabel(ctx context.Context, deps Deps, pane herdrcli.Pane) error {
	return deps.Client.ReportMetadata(ctx, pane.PaneID, herdrcli.ReportMetadataOptions{
		Source:           metadataSource,
		ClearStateLabels: true,
	})
}
