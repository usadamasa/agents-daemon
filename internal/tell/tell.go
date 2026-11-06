// Package tell は、生きている Claude Code のセッションの pane の入力欄に文面を打って Enter を送る (#364)。
//
// 依存図のアプリ (gui/) が利用者の押した操作をセッションへ届ける経路で、`agents-daemon tell` が呼ぶ。利用者の操作は
// 利用者の指示なので、peer メッセージではなく入力欄に打った文面としてそのターンの指示にする。アプリはどのセッションにも
// 属さず engine の送信を持たないので、多重化ツールへの操作は mod や hook から叩かない決めごとどおり daemon のバイナリが担う。
//
// 送る前に pane を読み、利用者の答えを待つ prompt が開いている (Enter が承認になる)、上限の画面 (monitor の再開の経路と
// 取り違える)、入力欄に打ちかけの文がある (後ろに連結される) のどれかなら送らない。Claude Code が入力欄に出す次の prompt の
// 提案 (薄字) は打つと消えるので、打ちかけの文には数えない。上限からの再開 (monitor) と違って
// Escape は送らない。Escape は `/rate-limit-options` のメニューを閉じるためのもので、ターンの途中のセッションに送ると
// 中断になる。ターンの途中の pane には送り、文面は入力欄に溜まって次のターンになる。
//
// 多重化ツールの口 (Host) は multiplexer.Client に足さず、ここに閉じる (spawn.Host と同じ考え)。
package tell

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// 既定値。monitor の送信 (config の SubmitDelayMs / ReadLines) と同じ。
const (
	defaultSubmitDelay = 400 * time.Millisecond
	defaultReadLines   = 25
)

// bracketed paste の囲み。Claude Code は paste mode (2004) を有効にしていて、囲んだ文面は改行を Enter と読まずに
// 1 つの貼り付けとして入力欄に入る。
// cc-premise: 2.1.319 input-line 複数行の文面は bracketed paste で 1 つの貼り付けになる
const (
	pasteBegin = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// Host は送る先の多重化ツール。rexcli の Client が満たす。
type Host interface {
	// PaneGet は pane の今の前景と自己申告 (multiplexer.Client と同じ)。
	PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error)
	// PaneRead は pane の画面 (同上)。
	PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error)
	// SendText は pane へリテラルテキストを送る (Enter は押さない)。
	SendText(ctx context.Context, paneID, text string) error
	// SendKeys は pane へキー入力を送る ("enter")。
	SendKeys(ctx context.Context, paneID string, keys ...string) error
}

// Request は送る文面と宛先。
type Request struct {
	// ToSession は宛先のセッションの ID。
	ToSession string
	// Text は入力欄に打つ文面。
	Text string
}

// Result は送った結果。JSON で stdout に出す。形は protocol の schema が持つ。
type Result = protocol.TellResult

// Runner は送る手段。テストは Host と Blocks と Screen を差し替える。
type Runner struct {
	Host Host
	// Blocks は block の ID から session_id への対応 (sessioncontext.Store.ListMultiplexerBlocks)。
	Blocks func() (map[string]string, error)
	// Screen は画面 (か文面) が上限の画面のどの種類に当たるかの名前 (detect.Kind の String)。当たらなければ空。
	// nil なら調べない。
	Screen func(text string) string
	// Sleep は文面と Enter の間の待ち。nil なら time.Sleep。
	Sleep func(time.Duration)
	// SubmitDelay は文面と Enter の間の待ちの長さ。零なら既定。
	SubmitDelay time.Duration
	// ReadLines は画面を末尾から何行読むか。零なら既定。
	ReadLines int
}

// Run は req の文面を宛先のセッションの pane に打つ。送らなかったときは理由を error で返し、pane には何も送っていない。
func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	req.Text = stripControls(req.Text)
	if strings.TrimSpace(req.Text) == "" {
		return Result{}, errors.New("文面が空なので送らない")
	}
	pane, err := r.find(ctx, req.ToSession)
	if err != nil {
		return Result{}, err
	}
	if multiplexer.WaitingOnPrompt(pane) {
		return Result{}, fmt.Errorf("pane %s は利用者の答えを待つ prompt が開いている (%s) ので送らない", pane.PaneID, multiplexer.RecordLabel(pane))
	}
	lines := r.ReadLines
	if lines <= 0 {
		lines = defaultReadLines
	}
	screen, err := r.Host.PaneRead(ctx, pane.PaneID, multiplexer.PaneReadOptions{Lines: lines})
	if err != nil {
		return Result{}, fmt.Errorf("pane %s の画面を読めない: %w", pane.PaneID, err)
	}
	if kind := r.classify(screen); kind != "" {
		return Result{}, fmt.Errorf("pane %s は上限の画面 (%s) なので送らない", pane.PaneID, kind)
	}
	rest, found := inputRemainder(screen)
	if !found {
		return Result{}, fmt.Errorf("pane %s の画面に入力欄 (❯ の行) が見つからないので送らない", pane.PaneID)
	}
	if rest != "" {
		// 素の画面では次の prompt の提案と打ちかけの文が同じに見えるので、制御シーケンス付きで読み直して薄字を除く。
		styled, err := r.Host.PaneRead(ctx, pane.PaneID, multiplexer.PaneReadOptions{Lines: lines, Styled: true})
		if err != nil {
			return Result{}, fmt.Errorf("pane %s の画面を読めない: %w", pane.PaneID, err)
		}
		if typed := typedRemainder(styled); typed != "" {
			// 打ちかけの文は相手のセッションの中身なので、理由 (ログにも残る) には長さだけを書く。
			return Result{}, fmt.Errorf("pane %s の入力欄に打ちかけの文 (%d 文字) があるので送らない", pane.PaneID, utf8.RuneCountInString(typed))
		}
	}
	res := Result{PaneID: pane.PaneID}
	if kind := r.classify(req.Text); kind != "" {
		res.Warning = fmt.Sprintf("文面が上限の検知語 (%s) を含む。入力欄に残る間は daemon が上限と取り違えることがある", kind)
	}
	if err := r.submit(ctx, pane.PaneID, req.Text); err != nil {
		return Result{}, err
	}
	return res, nil
}

// find は session の block のうち claude が前景のものを返す。複数あれば ID の順で最後 (block の ID は時刻順なので
// 新しいもの)。
func (r Runner) find(ctx context.Context, sessionID string) (multiplexer.Pane, error) {
	blocks, err := r.Blocks()
	if err != nil {
		return multiplexer.Pane{}, fmt.Errorf("block と session の対応を読めない: %w", err)
	}
	var ids []string
	for id, sid := range blocks {
		if sid == sessionID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return multiplexer.Pane{}, fmt.Errorf("セッション %s の pane が見つからない (statusline が context を書いていないか、終了している)", sessionID)
	}
	sort.Strings(ids)
	var found *multiplexer.Pane
	for _, id := range ids {
		pane, err := r.Host.PaneGet(ctx, id)
		if err != nil {
			continue
		}
		if multiplexer.IsClaudeAgent(pane) {
			found = &pane
		}
	}
	if found == nil {
		return multiplexer.Pane{}, fmt.Errorf("セッション %s の pane (%s) で claude が前景でないので送らない", sessionID, strings.Join(ids, ", "))
	}
	return *found, nil
}

func (r Runner) classify(text string) string {
	if r.Screen == nil {
		return ""
	}
	return r.Screen(text)
}

// stripControls は文面の制御文字 (改行とタブ以外の C0、DEL、C1) を落とす。文面はメモ (セッションが park tool で書いた
// もの) や利用者の入力で、ESC が混じると pane ではキー (ターンの中断、paste の終わり) として読まれる。
func stripControls(text string) string {
	return strings.Map(func(c rune) rune {
		if c == '\n' || c == '\t' {
			return c
		}
		if c < 0x20 || (c >= 0x7f && c < 0xa0) {
			return -1
		}
		return c
	}, text)
}

// submit は文面と Enter を別々のキーストロークで送る (monitor の再開と同じ作法。まとめて送ると Claude のペースト検知に
// 引っかかる)。改行かタブを含む文面は bracketed paste で囲む (タブはそのまま打つと Claude Code のキーになる)。
func (r Runner) submit(ctx context.Context, paneID, text string) error {
	if strings.ContainsAny(text, "\n\t") {
		text = pasteBegin + text + pasteEnd
	}
	if err := r.Host.SendText(ctx, paneID, text); err != nil {
		return fmt.Errorf("pane %s に文面を打てない: %w", paneID, err)
	}
	sleep, delay := r.Sleep, r.SubmitDelay
	if sleep == nil {
		sleep = time.Sleep
	}
	if delay <= 0 {
		delay = defaultSubmitDelay
	}
	sleep(delay)
	if err := r.Host.SendKeys(ctx, paneID, "enter"); err != nil {
		return fmt.Errorf("pane %s に Enter を送れない (文面は入力欄に残っている): %w", paneID, err)
	}
	return nil
}

// promptMark は Claude Code の入力欄の行頭の印。
// cc-premise: 2.1.319 input-line 入力欄は ❯ で始まる行で、空のときは ❯ の後に NBSP (U+00A0) だけ
const promptMark = "❯"

// inputRemainder は画面の入力欄に残っている文を返す。見るのは ❯ で始まる最後の行 (入力欄は画面の下にあり、上の出力に
// ❯ が出ることもある)。空の入力欄は空白か NBSP だけなので、それを除いた残りを返す。❯ の行が無ければ found が false。
func inputRemainder(screen string) (rest string, found bool) {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimLeft(lines[i], " ")
		after, ok := strings.CutPrefix(line, promptMark)
		if !ok {
			continue
		}
		return strings.Trim(after, " \t "), true
	}
	return "", false
}

// typedRemainder は制御シーケンス付きの画面から、入力欄に利用者が打った文を返す。Claude Code は次の prompt の提案を
// 入力欄に薄字 (SGR 2) で出し、何か打つと消える (打った文の後ろに連結はされない) ので、薄字の文字は数えない。
// 提案の見た目が変わって薄字でなくなれば、打ちかけの文として拒む側に倒れる。
// cc-premise: 2.1.319 input-line 次の prompt の提案は入力欄に SGR 2 (薄字) で出て、打つと消える
func typedRemainder(styled string) string {
	var line string
	for _, l := range strings.Split(styled, "\n") {
		if strings.Contains(l, promptMark) {
			line = l
		}
	}
	var typed strings.Builder
	dim := false
	for len(line) > 0 {
		if strings.HasPrefix(line, "\x1b[") {
			end := strings.IndexFunc(line[2:], func(c rune) bool { return c >= 0x40 && c <= 0x7e })
			if end < 0 {
				break
			}
			if line[2+end] == 'm' {
				dim = dimAfter(dim, line[2:2+end])
			}
			line = line[3+end:]
			continue
		}
		c, size := utf8.DecodeRuneInString(line)
		if !dim && c >= 0x20 && c != 0x7f {
			typed.WriteRune(c)
		}
		line = line[size:]
	}
	after, ok := strings.CutPrefix(strings.TrimLeft(typed.String(), " "), promptMark)
	if !ok {
		return ""
	}
	return strings.Trim(after, " \t ")
}

// dimAfter は SGR の引数 (ESC [ と m の間) を読んだ後の薄字の状態。2 で始まり、0 (空も同じ) か 22 で終わる。
func dimAfter(dim bool, params string) bool {
	for _, p := range strings.Split(params, ";") {
		switch p {
		case "2":
			dim = true
		case "", "0", "22":
			dim = false
		}
	}
	return dim
}
