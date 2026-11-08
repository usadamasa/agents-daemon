// Package detect は Claude Code のセッションが上限 (か一時エラー) で止まっているかを、transcript の末尾の entry から
// 分類し (ClassifyTurn)、止まった pane へ送るキーを選ぶために画面の通知欄を読む (ClassifyScreen)。
//
// 上限の判定を画面の文字列で行わないのは、pane の画面には Claude Code が描いた通知と、tool の出力に引用された任意の
// 文章が同じ平面に並ぶため。引用を字面で弾くことはできない (fixture は本物の文言を持つ)。判定の入力は Claude Code が
// 自分の会話として書く transcript にし、画面は「本体が今どの通知を出しているか」を見るためだけに使う (#257)。
package detect

import (
	"regexp"
	"slices"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// Kind は ClassifyTurn の分類結果。セッションの最後の turn がどう終わったか。
type Kind int

const (
	// KindNone は上限にも一時エラーにも該当しない (turn が普通に終わった、まだ始まっていない、daemon が扱わない error)。
	KindNone Kind = iota
	// KindLimit は 5 時間ウィンドウの利用上限に当たり、解除を待つべき状態。
	KindLimit
	// KindWeeklyLimit は週のウィンドウ (seven_day 系) の上限に当たった状態。解除は数日先になりうる。
	KindWeeklyLimit
	// KindTransient は 5xx や過負荷、アカウント全体の rate limit (429 の Request rejected) で、短い待機で再試行すべき状態。
	KindTransient
	// KindSpendLimit はウィンドウの解除を待っても解けない上限 (spend limit / usage credit、Fable のモデル単位の上限)。
	// 待機に入ってはいけない。
	KindSpendLimit
)

// String は Kind のログ・JSON 向けの識別子。
func (k Kind) String() string {
	switch k {
	case KindLimit:
		return "limit"
	case KindWeeklyLimit:
		return "weekly-limit"
	case KindTransient:
		return "transient"
	case KindSpendLimit:
		return "spend-limit"
	default:
		return "none"
	}
}

// Limited は daemon が待機に入る種類 (解除か回復を待って送る) かを返す。
func (k Kind) Limited() bool {
	return k == KindLimit || k == KindWeeklyLimit || k == KindTransient
}

// transientErrors は Claude Code が API error を分類した語のうち、短い待機で回復しうるもの。
// cc-premise: 2.1.319 transient API error の error は rate_limit / overloaded / server_error (ほかに invalid_request、billing_error、unknown)
var transientErrors = []string{"overloaded", "server_error"}

// hardCapPatterns は待っても解除されない上限の文言 (entry の本文)。2.1.251 の Fable の上限は quotaLimits が無く、
// 本文が `You've reached your Fable 5 limit. Run /usage-credits to continue …` の形 (実物)。spend limit と usage credit は
// 2.1.239 の画面の文言 (native_continue_test.go)。
// cc-premise: 2.1.319 limit-screen spend limit / usage credit / Fable の上限の文言
var hardCapPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bspend limit\b`),
	regexp.MustCompile(`(?i)usage credit limit`),
	regexp.MustCompile(`(?i)credit balance (?:is )?too low`),
	regexp.MustCompile(`(?i)out of credits\b`),
	regexp.MustCompile(`(?i)(?:hit|reached) your fable(?: \S+)? limit|\bfable limit reached\b`),
}

// apiErrorTextPattern は Claude Code が上限の文ではなく生の API error として描いた本文の接頭辞。
// rate_limit でも quotaLimits が無く本文がこの形なら、アカウント全体の rate limit (429 の Request rejected) で、
// ウィンドウの解除ではなく短い待機で回復する (実物: 2.1.233 〜 2.1.283 の 9 件)。
var apiErrorTextPattern = regexp.MustCompile(`^\s*API Error\b`)

// ClassifyTurn は transcript の末尾の会話の entry から、セッションが上限で止まっているかを分類する。
// nil と、assistant 以外、API error でない entry は KindNone。
//
//   - error が rate_limit で quotaLimits に resetsAt があれば、rateLimitType が seven_day 系なら KindWeeklyLimit、
//     それ以外 (five_hour) は KindLimit
//   - error が rate_limit で quotaLimits が無ければ本文で分ける。spend limit / credits / Fable の形なら KindSpendLimit、
//     `API Error` で始まれば KindTransient。どちらでもなければ KindNone (2.1.233 以前の形は追わない。解除時刻が無いので
//     待てず、見逃しは StopFailure の記録との食い違いのログで気づく)
//   - error が overloaded / server_error なら KindTransient
//   - それ以外の error (invalid_request、billing_error、unknown) は daemon が送って直るものではないので KindNone
func ClassifyTurn(e *transcript.Entry) Kind {
	if e == nil || e.Type != "assistant" || e.Error == "" {
		return KindNone
	}
	if slices.Contains(transientErrors, e.Error) {
		return KindTransient
	}
	if e.Error != "rate_limit" {
		return KindNone
	}
	if e.Quota != nil && !e.Quota.ResetsAt.IsZero() {
		if strings.HasPrefix(e.Quota.RateLimitType, "seven_day") {
			return KindWeeklyLimit
		}
		return KindLimit
	}
	switch {
	case matchesAny(e.Text, hardCapPatterns):
		return KindSpendLimit
	case apiErrorTextPattern.MatchString(e.Text):
		return KindTransient
	default:
		return KindNone
	}
}

// Hint は ClassifyScreen の結果。上限で止まった pane の画面に、Claude Code 本体がどの通知を出しているか。
// 送るキーを選ぶためだけに使い、上限かどうかの判定には使わない。
type Hint int

const (
	// HintNone は通知欄に何も無い。通常の再開 (esc → 文面 → enter) を送る。
	HintNone Hint = iota
	// HintNativeRetrying は Claude Code が request の中でリトライしている (unattended retry mode)。手を出さない。
	HintNativeRetrying
	// HintNativeArmed は Claude Code 自身の auto-continue が解除を待っている。Escape が取り消しキーなので手を出さない。
	HintNativeArmed
	// HintResumeReady は上限が解除済みで、Claude Code が Enter だけを待っている。テキストを打つと中断された turn ではなく
	// 新しい依頼になるため、Enter だけを送る。
	HintResumeReady
	// HintLowerPriority は `/low-priority` で低優先度の作業を続けている。pane は動いているので手を出さない (#251)。
	HintLowerPriority
)

// String は Hint のログ・JSON 向けの識別子。
func (h Hint) String() string {
	switch h {
	case HintNativeRetrying:
		return "native-retrying"
	case HintNativeArmed:
		return "native-armed"
	case HintResumeReady:
		return "resume-ready"
	case HintLowerPriority:
		return "lower-priority"
	default:
		return "none"
	}
}

var (
	// ANSI エスケープシーケンス除去用。多重化ツールの capture は除去済みのテキストを返すが、生テキストにも備える。
	oscEscapeRe   = regexp.MustCompile(`\x1b\][\s\S]*?(?:\x07|\x1b\\)`)
	dcsEscapeRe   = regexp.MustCompile(`\x1bP[\s\S]*?(?:\x07|\x1b\\)`)
	otherEscapeRe = regexp.MustCompile(`\x1b[_X^][\s\S]*?(?:\x07|\x1b\\)`)
	csiEscapeRe   = regexp.MustCompile(`\x1b\[[\x20-\x3f]*[\x40-\x7e]`)

	// autoContinueArmedPattern は Claude Code 自身の auto-continue (2.1.234 の autoContinueAtUsageLimit、既定 on) が
	// カウントダウン中に transcript へ足す 1 行。文言は 6 種あり、どれも「usage limit の話 → continuing …」の並びになる。
	// 単に "continuing automatically" とだけ書かれた無関係な文章では発火しないよう、同じ行に上限の語を要求する。
	// この状態では Escape が取り消しキーなので、検知したら手を出さない。
	// cc-premise: 2.1.299 limit-screen auto-continue のバナーと降りた通知の文言 (native_continue_test.go)
	autoContinueArmedPattern = regexp.MustCompile(`(?i)(?:usage limit|limit (?:has )?reset).*\bcontinuing (?:automatically|shortly|now)\b`)

	// nativeStoodDownPattern はネイティブが継続を諦めたことを伝える通知。予約中のバナーは画面下部で描き直される要素なので、
	// 取り消されても直前のフレームがスクロールバックに残ることがある。古いバナーを見て手を引き続けると、ネイティブも
	// 動かずこのツールも動かない状態で固まる。そのため予約の判定より先に見る。
	nativeStoodDownPattern = regexp.MustCompile(`(?i)automatic continue (?:cancelled|stopped|was turned off|did not run)`)

	// lowerPriorityActivePattern は session limit の後に `/low-priority` で低優先度の作業を続けていることを示す。
	// cc-premise: 2.1.310 limit-screen lower-priority mode の作業中と終わりの通知 (lower_priority_test.go)
	lowerPriorityActivePattern = regexp.MustCompile(`(?i)continuing now at lower priority|lower-priority mode is back on|^\s*lower priority until\b|working at lower priority`)

	// lowerPriorityEndedPattern は lower-priority mode が終わり、通常の上限に戻ったことを示す。
	// 作業中の通知は scrollback に残るので、同じ画面にあればこちらを優先する。
	lowerPriorityEndedPattern = regexp.MustCompile(`(?i)lower-priority mode (?:ended|stopped|is off|is no longer available|isn't available|is taking a break)`)

	// nativeRetry* は Claude Code が request の中でリトライしている間のスピナー行 (`⚠ … · Retrying in 3h 12m (5pm) ·
	// attempt 4/10`)。unattended retry mode (`CLAUDE_CODE_RETRY_WATCHDOG`) は usage limit の解除もこの形で待ち、
	// Escape で待機ごと取り消される。上限の呼び名と時刻の書式に頼らず、2 つの区切りだけを見る。狭い pane では
	// attempt が次の行へ折り返される。
	// cc-premise: 2.1.316 limit-screen retry のスピナー行 (バイナリのテンプレート、実機未確認。native_retry_test.go)
	nativeRetryPattern        = regexp.MustCompile(`(?i)·\s*retrying in\b`)
	nativeRetryAttemptPattern = regexp.MustCompile(`(?i)\battempt \d+/\S+`)

	// resumeReadyPattern は上限が解除済みで Enter だけを待っている状態を transcript へ足す 1 行。
	// ネイティブが解除まで待ったものの継続の窓を過ぎた (phase=stale) ときに出る。
	resumeReadyPattern = regexp.MustCompile(`(?i)usage limit has reset.*press enter to continue`)

	// footer* は上限で待っている間の通知欄。status の行と、字下げした next の行に分けて描かれる。transcript に足される
	// 1 行の文 (上の 2 つ) が走査範囲の外へ出ると、画面に残るのはこの 2 行だけになる。引用された文で発火しないよう、
	// どちらも行頭から一致させる (status の頭の記号 1 つは許す)。
	// cc-premise: 2.1.310 limit-screen 通知欄は status と next を別の行に描く (footer_test.go)
	footerArmedStatusPattern = regexp.MustCompile(`(?i)^\s*(?:[^\w\s]\s*)?usage limit reached\b`)
	footerArmedNextPattern   = regexp.MustCompile(`(?i)^\s*continuing (?:automatically|shortly)\b`)
	footerStaleStatusPattern = regexp.MustCompile(`(?i)^\s*(?:[^\w\s]\s*)?your usage limit has reset\s*$`)
	footerStaleNextPattern   = regexp.MustCompile(`(?i)^\s*press enter to continue\s*(?:·|$)`)
)

// ClassifyScreen は画面の末尾 n 行 (0 以下なら全行) から、Claude Code 本体の通知欄を読む。
// 優先順位は次のとおり。上のものほど「このツールが手を出してはいけない」度合いが高い:
//
//  1. request の中でリトライ中 (HintNativeRetrying)。本体が待っているので、ほかの何よりも優先する
//  2. ネイティブ auto-continue がカウントダウン中 (HintNativeArmed)。取り消しの通知が同じ画面にあれば予約中と見なさない
//  3. 解除済みで Enter 待ち (HintResumeReady)
//  4. lower-priority mode で作業中 (HintLowerPriority)。終わった通知が同じ画面にあれば作業中と見なさない
func ClassifyScreen(screen string, n int) Hint {
	lines := tailLines(splitScreen(screen), n)

	if nativeRetrying(lines) {
		return HintNativeRetrying
	}
	stoodDown := slices.ContainsFunc(lines, nativeStoodDownPattern.MatchString)
	armed := slices.ContainsFunc(lines, autoContinueArmedPattern.MatchString) ||
		hasFooterPair(lines, footerArmedStatusPattern, footerArmedNextPattern)
	if !stoodDown && armed {
		return HintNativeArmed
	}
	if slices.ContainsFunc(lines, resumeReadyPattern.MatchString) ||
		hasFooterPair(lines, footerStaleStatusPattern, footerStaleNextPattern) {
		return HintResumeReady
	}
	if lowerPriorityActive(lines) {
		return HintLowerPriority
	}
	return HintNone
}

// stripANSI は ANSI エスケープシーケンスを除去する。
func stripANSI(s string) string {
	s = oscEscapeRe.ReplaceAllString(s, "")
	s = dcsEscapeRe.ReplaceAllString(s, "")
	s = otherEscapeRe.ReplaceAllString(s, "")
	s = csiEscapeRe.ReplaceAllString(s, "")
	return s
}

// splitScreen は画面テキストを ANSI 除去のうえ行へ分割する。
func splitScreen(screen string) []string {
	return strings.Split(stripANSI(screen), "\n")
}

// tailLines は lines の末尾 n 行を返す。n が 0 以下なら lines をそのまま返す。
func tailLines(lines []string, n int) []string {
	if n > 0 && len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

func matchesAny(s string, patterns []*regexp.Regexp) bool {
	for _, p := range patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// footerPairWindow は通知欄の status の行から next の行までの行数の上限。狭い pane では status が折り返される。
const footerPairWindow = 2

// hasFooterPair は status に一致する行の後 footerPairWindow 行以内に next に一致する行があるかを返す。
func hasFooterPair(lines []string, status, next *regexp.Regexp) bool {
	for i, line := range lines {
		if !status.MatchString(line) {
			continue
		}
		end := min(i+1+footerPairWindow, len(lines))
		if slices.ContainsFunc(lines[i+1:end], next.MatchString) {
			return true
		}
	}
	return false
}

// nativeRetrying は `· Retrying in` の後ろ (同じ行か、折り返した次の行) に `attempt n/m` があるかを返す。
func nativeRetrying(lines []string) bool {
	for i, line := range lines {
		loc := nativeRetryPattern.FindStringIndex(line)
		if loc == nil {
			continue
		}
		if nativeRetryAttemptPattern.MatchString(line[loc[1]:]) ||
			(i+1 < len(lines) && nativeRetryAttemptPattern.MatchString(lines[i+1])) {
			return true
		}
	}
	return false
}

// lowerPriorityActive は lower-priority mode の作業中の通知があり、終わった通知が無いかを返す。
func lowerPriorityActive(lines []string) bool {
	return slices.ContainsFunc(lines, lowerPriorityActivePattern.MatchString) &&
		!slices.ContainsFunc(lines, lowerPriorityEndedPattern.MatchString)
}
