// Package detect は Claude Code pane の画面テキストを分類する。
// 多重化ツールの capture は ANSI 除去済みのテキストを返すが、
// このパッケージは呼び出し元が生テキストを渡してくる場合にも備えて ANSI を自前で除去する。
package detect

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kind は Classify の分類結果を表す。
type Kind int

const (
	// KindNone は上限にも一時的なエラーにも該当しない。
	KindNone Kind = iota
	// KindLimit はサブスクリプション/利用上限に達しており、解除を待つべき状態。
	KindLimit
	// KindTransient は 5xx やコネクションエラー等、短い待機で再試行すべき状態。
	KindTransient
	// KindAutoContinueArmed は Claude Code 自身の auto-continue
	// (2.1.234 の autoContinueAtUsageLimit) が解除を待っている状態。
	// このツールは手を出さない。
	KindAutoContinueArmed
	// KindResumeReady は上限が解除済みで、Claude Code が Enter だけを待っている状態。
	// テキストを打ち込むと中断された turn ではなく新しい依頼になるため、Enter だけを送る。
	KindResumeReady
	// KindSpendLimit は 5 時間ウィンドウの解除を待っても解けない上限 (月次 spend limit / usage credit、
	// Fable の週次上限) に当たっている状態。待機に入ってはいけない。
	KindSpendLimit
)

// String は Kind の日本語ではない識別子文字列を返す (ログ・JSON 出力向け)。
func (k Kind) String() string {
	switch k {
	case KindLimit:
		return "limit"
	case KindTransient:
		return "transient"
	case KindAutoContinueArmed:
		return "auto-continue-armed"
	case KindResumeReady:
		return "resume-ready"
	case KindSpendLimit:
		return "spend-limit"
	default:
		return "none"
	}
}

// nearbyWindow は limit フレーズの前後何行以内にリセット時刻フレーズがあれば
// 「上限に達した」と判定するかの範囲。参考実装 (patterns.js) の WINDOW に合わせる。
const nearbyWindow = 6

// MinDetectableRows は上限の画面を limit と判定できる block の高さの下限 (行)。Claude Code は alternate screen で
// 描き、多重化ツールの capture は可視の行しか返さないので、上限の文 (画面の下から 10 行目) がこれより低い block では
// 画面の外へ出て検知が黙って効かなくなる。実測は pane-io.md「読み取り元」(statusline 3 行。1 行少なければ下限も
// 1 行下がる)。doctor が pane の高さと比べる。
// cc-premise: 2.1.302 limit-screen 上限の文は画面の下から 10 行目に出る (statusline 3 行)
const MinDetectableRows = 11

var (
	// ANSI エスケープシーケンス除去用。参考実装 (patterns.js stripAnsi) を移植。
	oscEscapeRe   = regexp.MustCompile(`\x1b\][\s\S]*?(?:\x07|\x1b\\)`)
	dcsEscapeRe   = regexp.MustCompile(`\x1bP[\s\S]*?(?:\x07|\x1b\\)`)
	otherEscapeRe = regexp.MustCompile(`\x1b[_X^][\s\S]*?(?:\x07|\x1b\\)`)
	csiEscapeRe   = regexp.MustCompile(`\x1b\[[\x20-\x3f]*[\x40-\x7e]`)

	// 上限フレーズ。単体では発火せず、nearbyWindow 以内に resetPatterns が要る。
	// cc-premise: 2.1.299 limit-screen 上限到達の文言 (real_sample_test.go の現物)
	limitPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:hit|exceeded|reached).*(?:your|the)\s*(?:[\w-]+\s+){0,3}limit`),
		regexp.MustCompile(`(?i)\d+-hour limit`),
		regexp.MustCompile(`(?i)session limit`),
		regexp.MustCompile(`(?i)weekly limit`),
		regexp.MustCompile(`(?i)limit reached`),
		regexp.MustCompile(`(?i)usage limit`),
		regexp.MustCompile(`(?i)out of.*usage`),
		regexp.MustCompile(`(?i)rate limit`),
		regexp.MustCompile(`(?i)try again in`),
	}

	// usageWarningPattern に一致する行は「85% of your 5-hour limit」のような
	// 進捗表示であり、上限到達の警告ではない。limitPatterns の判定より先に除外する。
	usageWarningPattern = regexp.MustCompile(`(?i)\b\d{1,3}%\s+of your\b`)

	// autoContinueArmedPattern は Claude Code 自身の auto-continue
	// (2.1.234 の autoContinueAtUsageLimit、既定 on) がカウントダウン中であることを示す。
	// 実際の文言は "Usage limit reached · continuing automatically at 5:00pm ·
	// esc or type to cancel" 等 6 種あり、どれも「usage limit の話 → continuing …」の
	// 並びになる。この 1 本でまとめて拾い、単に "continuing automatically" とだけ
	// 書かれた無関係な文章では発火しないよう、同じ行に上限の語を要求する。
	//
	// この状態では **Escape が取り消しキー** なので、待機明けに Escape を送ると
	// ネイティブの継続そのものを潰す。だで検知したら手を出さない。
	// cc-premise: 2.1.299 limit-screen auto-continue のバナーと降りた通知の文言 (native_continue_test.go)
	autoContinueArmedPattern = regexp.MustCompile(`(?i)(?:usage limit|limit (?:has )?reset).*\bcontinuing (?:automatically|shortly|now)\b`)

	// nativeStoodDownPattern はネイティブが継続を諦めたことを伝える通知。
	// 予約中のバナーは画面下部で描き直される要素なので、取り消されても直前の
	// フレームがスクロールバックに残ることがある。古いバナーを見て手を引き続けると、
	// ネイティブも動かずこのツールも動かない状態で固まる。そのため予約の判定より先に見る。
	nativeStoodDownPattern = regexp.MustCompile(`(?i)automatic continue (?:cancelled|stopped|was turned off|did not run)`)

	// lowerPriorityActivePattern は session limit の後に `/low-priority` で低優先度の作業を続けていることを示す。
	// 通知は「limit resets at 5pm」と「weekly limit」を含み二重シグナルを満たすので、pane は作業中でも limit になる。
	// cc-premise: 2.1.310 limit-screen lower-priority mode の作業中と終わりの通知 (lower_priority_test.go)
	lowerPriorityActivePattern = regexp.MustCompile(`(?i)continuing now at lower priority|lower-priority mode is back on|^\s*lower priority until\b|working at lower priority`)

	// lowerPriorityEndedPattern は lower-priority mode が終わり、通常の上限に戻ったことを示す。
	// 作業中の通知は scrollback に残るので、同じ画面にあればこちらを優先する。
	lowerPriorityEndedPattern = regexp.MustCompile(`(?i)lower-priority mode (?:ended|stopped|is off|is no longer available|isn't available|is taking a break)`)

	// resumeReadyPattern は上限が解除済みで Enter だけを待っている状態を示す。
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

	// hardCapPatterns は待っても解除されない上限を示す。2.1.239 で月次 spend limit の
	// メッセージにも session / weekly のリセット時刻が載るようになり、二重シグナル
	// 要件を満たすようになった。待機に入ると解除時刻に送信 → 再び弾かれる、を
	// maxRetries まで繰り返す。上流も retry watchdog 側で同じ扱いにしている (2.1.232)。
	// cc-premise: 2.1.299 limit-screen spend limit / usage credit の文言
	hardCapPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bspend limit\b`),
		regexp.MustCompile(`(?i)usage credit limit`),
		regexp.MustCompile(`(?i)credit balance (?:is )?too low`),
		regexp.MustCompile(`(?i)out of credits\b`),
		// Fable のモデル単位の週次上限は 5 時間ウィンドウの解除では解けない。上限のダイアログは Enter が
		// 使用クレジットで続ける選択肢を選ぶ。使用率の警告 (`85% of your Fable limit`) は拾わない。
		// cc-premise: 2.1.316 limit-screen Fable の上限の行とダイアログの文言 (fable_limit_test.go)
		regexp.MustCompile(`(?i)(?:hit|reached) your fable limit|\bfable limit reached\b`),
	}

	// resetPatterns は limitPatterns の近傍にあることで「上限に達した」の裏取りになる。
	resetPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?\d{1,2}(?::\d{2})?\s*(?:am|pm)?`),
		regexp.MustCompile(`(?i)resets?\s+in[:\s]\s*\d`),
		regexp.MustCompile(`(?i)try again in \d+\s*(?:hours?|minutes?|h|m)`),
	}

	// transientAnchorPattern は Claude Code がリトライを諦めた API の失敗を描く行の接頭辞。
	// transient の語句はこの接頭辞と同じ行にあるときだけ拾う。直近の出力ブロックには tool result の
	// 任意の文章も入るので、裸の語句では issue のタイトルやログの引用に当たる。
	// リトライ中のカウントダウン行には接頭辞が付かず、その間は Claude Code に任せる。
	// cc-premise: 2.1.296 transient API Error 行の接頭辞とリトライ表示 (detect_test.go)
	transientAnchorPattern = regexp.MustCompile(`(?i)\bAPI Error\b`)

	// transientPatterns は直近の出力ブロック内 (latestOutputBlock) の、transientAnchorPattern と
	// 同じ行でのみ判定する。
	transientPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)temporarily limiting requests`),
		regexp.MustCompile(`(?i)\brate limited\b`),
		regexp.MustCompile(`(?i)\boverloaded\b`),
		regexp.MustCompile(`(?i)api error:?\s*(?:5\d\d|429)\b`),
		regexp.MustCompile(`(?i)internal server error`),
		regexp.MustCompile(`(?i)server[-\s]side issue`),
		regexp.MustCompile(`(?i)api error:?\s*connection\b`),
	}

	// 直近の出力ブロックの範囲を決める境界線。参考実装の OUTPUT_LINE / NON_OUTPUT_LINE / THINKING_LINE を移植。
	outputLineRe    = regexp.MustCompile(`^\s*[⏺⎿]`)
	nonOutputLineRe = regexp.MustCompile(`^\s*[⏺⎿❯>]`)
	thinkingLineRe  = regexp.MustCompile(`(?i)^\s*\S{0,2}\s*\w+\s+for\s+\d+m?\s?\d*s\b`)

	// ParseRelativeWait が使う相対時刻表現。絶対時刻 (resets at 3pm 等) は
	// limitstate から得る resets_at epoch で代替できるため移植しない。
	relativeWaitRe = regexp.MustCompile(`(?i)(?:try again|wait|resets?\s+in)[:\s]\s*(?:for\s+)?(?:in\s+)?(\d+)\s*(hours?|minutes?|mins?|h|m)\b`)
)

// stripANSI は ANSI エスケープシーケンスを除去する。
// 多重化ツールの capture は除去済みテキストを返すが、
// 呼び出し元が生テキストを渡してくる場合に備えて防御的に処理する。
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

// hasNearbyMatch は lines[idx] の前後 window 行以内 (idx 自身を含む) に
// patterns のいずれかへ一致する行があるかを返す。
func hasNearbyMatch(lines []string, idx, window int, patterns []*regexp.Regexp) bool {
	start := max(idx-window, 0)
	end := min(idx+window+1, len(lines))
	for j := start; j < end; j++ {
		if matchesAny(lines[j], patterns) {
			return true
		}
	}
	return false
}

// latestOutputBlock は画面末尾から直近の出力ブロック (⏺/⎿ で始まる行から、
// 空行・プロンプト行・"thought for Ns" 行の直前までの連続) を取り出す。
// 見つからない場合は nil を返す。
//
// transient 判定をこのブロックだけに絞ることで、画面上部に残った古いエラー文言が
// 再発火するのを防ぐ。
func latestOutputBlock(lines []string) []string {
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if outputLineRe.MatchString(lines[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}

	block := []string{lines[start]}
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || nonOutputLineRe.MatchString(line) || thinkingLineRe.MatchString(line) {
			break
		}
		block = append(block, line)
	}
	return block
}

// Classifier は設定ファイル由来の追加 limit パターンを保持し、画面テキストを分類する。
// transient の語句は組み込みだけで、設定からは足せない (Claude Code 自身のエラー行の
// 言い回しに閉じているため、利用者が足す理由が無い)。
type Classifier struct {
	extraLimit []*regexp.Regexp
}

// NewClassifier は追加 limit パターン (設定ファイル由来の文字列) をコンパイルして Classifier を作る。
// Claude Code の文言は安定した API ではないため、コード変更無しに言い回しを足せるようにする。
// 不正な正規表現が含まれる場合はエラーを返す。
func NewClassifier(extraLimitPatterns []string) (*Classifier, error) {
	limit, err := compilePatterns(extraLimitPatterns)
	if err != nil {
		return nil, fmt.Errorf("追加 limit パターンのコンパイルに失敗: %w", err)
	}
	return &Classifier{extraLimit: limit}, nil
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("パターン %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

// Classify は screen を分類する。n には画面末尾から対象にする行数を渡す
// (画面のフッター部分)。0 以下なら全行を対象にする。
//
// 上限判定には二重のシグナルを要求する: limitPatterns (または追加 limit パターン) に
// 一致する行があり、かつその近傍 (nearbyWindow 行以内) に resetPatterns が一致する行が
// あって初めて KindLimit を返す。上限フレーズ単体では発火させない (通常出力・この計画書
// のような文字列自体での誤爆を防ぐ)。
// 優先順位は次のとおり。上のものほど「このツールが手を出してはいけない」度合いが高い:
//
//  1. ネイティブ auto-continue がカウントダウン中 (KindAutoContinueArmed)
//  2. 解除済みで Enter 待ち (KindResumeReady)
//  3. 二重シグナルが揃っており、かつ恒久的な上限 (KindSpendLimit)
//  4. 二重シグナルが揃った通常の上限 (KindLimit)
//  5. 直近出力ブロックの `API Error` 行にある一時エラー (KindTransient)
func (c *Classifier) Classify(screen string, n int) Kind {
	lines := tailLines(splitScreen(screen), n)

	stoodDown := slices.ContainsFunc(lines, nativeStoodDownPattern.MatchString)
	armed := slices.ContainsFunc(lines, autoContinueArmedPattern.MatchString) ||
		hasFooterPair(lines, footerArmedStatusPattern, footerArmedNextPattern)
	if !stoodDown && armed {
		return KindAutoContinueArmed
	}
	if slices.ContainsFunc(lines, resumeReadyPattern.MatchString) ||
		hasFooterPair(lines, footerStaleStatusPattern, footerStaleNextPattern) {
		return KindResumeReady
	}

	if c.isLimit(lines) {
		// 恒久的な上限の判定は「limit が成立した画面」に限る。単に spend limit の
		// 語を含むだけの画面まで拾うと、transient の検知まで巻き込んで潰してしまう。
		for _, line := range lines {
			if matchesAny(line, hardCapPatterns) {
				return KindSpendLimit
			}
		}
		return KindLimit
	}
	if c.isTransient(lines) {
		return KindTransient
	}
	return KindNone
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

// LowerPriorityActive は画面の末尾 n 行 (Classify と同じ範囲) に lower-priority mode の作業中の通知があり、
// 終わった通知が無いかを返す。判定には使わず、上限の出来事に印を付けるためだけに使う (#251)。
func LowerPriorityActive(screen string, n int) bool {
	lines := tailLines(splitScreen(screen), n)
	return slices.ContainsFunc(lines, lowerPriorityActivePattern.MatchString) &&
		!slices.ContainsFunc(lines, lowerPriorityEndedPattern.MatchString)
}

func (c *Classifier) isLimit(lines []string) bool {
	for i, line := range lines {
		if usageWarningPattern.MatchString(line) {
			continue
		}
		// 参考実装 (patterns.js) は設定由来の追加 limit パターンを近傍リセット判定なしで
		// 即座に確定させるが、ここでは意図的に採用しない。monitor の発火ゲートは
		// statusline 由来の五時間ウィンドウ使用率だが、その state が欠落/陳腐化している間は
		// ゲートが開いたままになる。そのときに追加パターンだけが単一シグナルで発火できると、
		// 裏取りの無い誤検知に直結する。組み込みパターンと同じ二重シグナル要件に統一するのが
		// 安全側のデフォルト。
		if !matchesAny(line, limitPatterns) && !matchesAny(line, c.extraLimit) {
			continue
		}
		if hasNearbyMatch(lines, i, nearbyWindow, resetPatterns) {
			return true
		}
	}
	return false
}

func (c *Classifier) isTransient(lines []string) bool {
	block := latestOutputBlock(lines)
	if block == nil {
		block = lines
	}
	for _, line := range block {
		if transientAnchorPattern.MatchString(line) && matchesAny(line, transientPatterns) {
			return true
		}
	}
	return false
}

// absoluteResetRe は "resets 8:10am (Asia/Tokyo)" / "resets at 12:30pm" のような
// 絶対時刻表現。am/pm を必須にして 24 時間表記は読まない (Claude Code の描画は
// 常に am/pm 付きで、am/pm の無い数字は時刻以外の何かである可能性が高い)。
// 括弧内の tz 名は任意。
var absoluteResetRe = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b(?:\s*\(([A-Za-z_]+(?:/[A-Za-z_+-]+)*)\))?`)

// maxAbsoluteResetAhead は画面の絶対時刻を「今日 / 明日」のどちらに解釈するかの上限。
// 5 時間ウィンドウの解除が 5 時間より先になることは無いので、これより先になる候補は
// 昨日 / 今日の同時刻 (=過去) と解釈する。
const maxAbsoluteResetAhead = 6 * time.Hour

// ParseAbsoluteReset は画面の末尾 n 行 (0 なら全行) から上限の解除時刻 (絶対時刻) を読む。
//
// 対象は Classify と同じ「limit 行の近傍 (nearbyWindow 行以内) にある reset 表現」だけ。
// 使用率の警告行 (`85% of your 5-hour limit · resets …`) は limit 行と見なさない。
// 複数あれば最も下 (最新の描画) を採る。
//
// 時刻には日付が無いので、昨日 / 今日 / 明日の候補のうち now+maxAbsoluteResetAhead を
// 超えない最も遅いものを返す。返る時刻は過去のこともあり (解除済み)、その扱いは
// 呼び出し元が決める。tz は括弧内の名前を time.LoadLocation で解決し、無い・解決
// できないときは local を使う。
func ParseAbsoluteReset(screen string, n int, now time.Time, local *time.Location) (time.Time, bool) {
	lines := tailLines(splitScreen(screen), n)

	var best time.Time
	found := false
	for i, line := range lines {
		if usageWarningPattern.MatchString(line) || !matchesAny(line, limitPatterns) {
			continue
		}
		for j := max(0, i-nearbyWindow); j < len(lines) && j <= i+nearbyWindow; j++ {
			m := absoluteResetRe.FindStringSubmatch(lines[j])
			if m == nil {
				continue
			}
			at, ok := resolveClockTime(m, now, local)
			if !ok {
				continue
			}
			// 走査は上から下なので、後に見つかったものほど画面の下にある。
			best, found = at, true
		}
	}
	return best, found
}

// resolveClockTime は absoluteResetRe の一致から具体的な時刻を組み立てる。
func resolveClockTime(m []string, now time.Time, local *time.Location) (time.Time, bool) {
	hour, err := strconv.Atoi(m[1])
	if err != nil || hour < 1 || hour > 12 {
		return time.Time{}, false
	}
	minute := 0
	if m[2] != "" {
		if minute, err = strconv.Atoi(m[2]); err != nil || minute > 59 {
			return time.Time{}, false
		}
	}
	// 12am は 0 時、12pm は 12 時。
	if hour == 12 {
		hour = 0
	}
	if strings.EqualFold(m[3], "pm") {
		hour += 12
	}

	loc := local
	if m[4] != "" {
		if named, locErr := time.LoadLocation(m[4]); locErr == nil {
			loc = named
		}
	}

	limit := now.Add(maxAbsoluteResetAhead)
	base := now.In(loc)
	var best time.Time
	for dayOffset := -1; dayOffset <= 1; dayOffset++ {
		candidate := time.Date(base.Year(), base.Month(), base.Day()+dayOffset, hour, minute, 0, 0, loc)
		if candidate.After(limit) {
			continue
		}
		if best.IsZero() || candidate.After(best) {
			best = candidate
		}
	}
	return best, !best.IsZero()
}

// maxRelativeWait は相対表現として読む待ち時間の上限。上限の文言が言う待ちは 5 時間ウィンドウでも週次でも
// これより短く、これを超える数は pane に流れた別の文章 (ログや tool result) で、掛け算で int64 を溢れて
// 負の待ちになる (fuzz_test.go の seed)。
const maxRelativeWait = 7 * 24 * time.Hour

// ParseRelativeWait は "try again in 12 minutes" / "resets in 2 hours" のような
// 相対的な待機時間の表現を screen から抽出する。見つからない場合は ok=false を返す。
//
// 絶対時刻表現 ("resets at 3pm" の時刻部分) はここでは解釈しない。statusline が書く
// state に厳密な resets_at (epoch 秒) があるため、その state が無い/古い場合の
// フォールバックとしてのみ相対表現を使う。
func ParseRelativeWait(screen string, n int) (time.Duration, bool) {
	lines := tailLines(splitScreen(screen), n)
	blob := strings.Join(lines, "\n")

	m := relativeWaitRe.FindStringSubmatch(blob)
	if m == nil {
		return 0, false
	}
	amount, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	unit := time.Hour
	if strings.HasPrefix(strings.ToLower(m[2]), "m") {
		unit = time.Minute
	}
	if amount > int(maxRelativeWait/unit) {
		return 0, false
	}
	return time.Duration(amount) * unit, true
}
