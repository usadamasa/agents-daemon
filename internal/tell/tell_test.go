package tell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

const (
	sid       = "6b1f3c2a-1111-4222-8333-944455556666"
	claudeID  = "block:01a15e6a-7fe3-7298-af51-8982a808f03b"
	shellID   = "block:01a15e6a-0000-7298-af51-8982a808f03b"
	otherSID  = "0a0a0a0a-2222-4333-8444-955566667777"
	otherID   = "block:01a15e6a-ffff-7298-af51-8982a808f03b"
	statusEnd = "  🐦 Fable 5.1 ｜ ⚡ high\n  📁 ~/src/github.com/usadamasa/agents-daemon ｜ 🌿 main ✔\n  ⏵⏵ auto mode on (shift+tab to cycle) · ⧉ 3\n"
)

// idleScreen は入力欄が空の画面 (rex capture --trim --unwrap の実測。空の入力行は ❯ の後に NBSP)。
const idleScreen = "⠼ Coalescing… (2m 29s · ↓ 9.3k tokens)\n" +
	"                                                   207790 tokens\n" +
	"────────────────────────────────────────\n" +
	"❯ \n" +
	"────────────────────────────────────────\n" + statusEnd

// typingScreen は利用者が打ちかけの文を残した画面。
const typingScreen = "────────────────────────────────────────\n" +
	"❯ ここまで打った\n" +
	"────────────────────────────────────────\n" + statusEnd

// 入力欄の行を制御シーケンス付き (rex capture --format=vt) で読んだもの。Claude Code の次の prompt の提案は
// SGR 2 (薄字) で描かれ、打ちかけの文は素の文字で描かれる。
const (
	emptyStyled      = "❯ \r\n"
	suggestionStyled = "❯ \x1b[0m\x1b[2mここまで打った\x1b[0m\r\n"
	typedStyled      = "❯ ここまで打った\r\n"
)

type fixture struct {
	host    *multiplexerfake.Client
	panes   map[string]multiplexer.Pane
	screen  string
	styled  string
	limited map[string]string
	hints   map[string]string
	slept   []time.Duration
}

func newFixture() *fixture {
	f := &fixture{
		panes: map[string]multiplexer.Pane{
			claudeID: {PaneID: claudeID, Agent: "claude-code", AgentStatus: multiplexer.AgentStatusIdle, SessionID: sid},
			shellID:  {PaneID: shellID, SessionID: sid},
			otherID:  {PaneID: otherID, Agent: "claude-code", AgentStatus: multiplexer.AgentStatusWorking, SessionID: otherSID},
		},
		screen:  idleScreen,
		styled:  emptyStyled,
		limited: map[string]string{},
		hints:   map[string]string{},
	}
	f.host = &multiplexerfake.Client{
		PaneGetFunc: func(_ context.Context, id string) (multiplexer.Pane, error) {
			p, ok := f.panes[id]
			if !ok {
				return multiplexer.Pane{}, errors.New("block が無い")
			}
			return p, nil
		},
		PaneReadFunc: func(_ context.Context, _ string, opts multiplexer.PaneReadOptions) (string, error) {
			if opts.Styled {
				return f.styled, nil
			}
			return f.screen, nil
		},
	}
	return f
}

func (f *fixture) runner() Runner {
	return Runner{
		Host: f.host,
		Blocks: func() (map[string]string, error) {
			return map[string]string{claudeID: sid, shellID: sid, otherID: otherSID}, nil
		},
		Limited:     func(sessionID string) string { return f.limited[sessionID] },
		Hint:        func(screen string) string { return f.hints[screen] },
		Sleep:       func(d time.Duration) { f.slept = append(f.slept, d) },
		SubmitDelay: 7 * time.Millisecond,
	}
}

func request(text string) Request { return Request{ToSession: sid, Text: text} }

// 成功: session の block のうち claude が前景のものへ、文面と Enter を別々に送る。1 行の文面はそのまま打つ。
func TestRunner_Run(t *testing.T) {
	f := newFixture()
	res, err := f.runner().Run(context.Background(), request("タスク #3 に着手してください。"))
	if err != nil {
		t.Fatal(err)
	}
	if res.PaneID != claudeID {
		t.Fatalf("result = %+v", res)
	}
	// block は ID の順に見る (shellID の方が古い)。
	// Enter の直前に pane をもう一度見る (#372)。
	want := []string{"PaneGet:" + shellID, "PaneGet:" + claudeID, "PaneRead:" + claudeID, "SendText:" + claudeID, "PaneGet:" + claudeID, "SendKeys:" + claudeID}
	if strings.Join(f.host.Calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v", f.host.Calls)
	}
	if f.host.SentTexts[0] != "タスク #3 に着手してください。" {
		t.Fatalf("sent = %q", f.host.SentTexts)
	}
	if len(f.slept) != 1 || f.slept[0] != 7*time.Millisecond {
		t.Fatalf("slept = %v", f.slept)
	}
}

// 複数行の文面は bracketed paste で囲んで 1 回で打つ (改行が Enter にならない)。
func TestRunner_Run_multilineIsPasted(t *testing.T) {
	f := newFixture()
	text := "メモをタスクにしてください。\n詳細: a\n(メモ x)"
	if _, err := f.runner().Run(context.Background(), request(text)); err != nil {
		t.Fatal(err)
	}
	if got := f.host.SentTexts[0]; got != "\x1b[200~"+text+"\x1b[201~" {
		t.Fatalf("sent = %q", got)
	}
}

// 文面の制御文字 (ESC、DEL、C1) は落として打つ。埋め込まれた bracketed paste の終わりの印も ESC が落ちて文字列になり、
// paste を途中で閉じない。改行とタブは残し、タブも paste で囲む (タブは Claude Code のキーになる)。
func TestRunner_Run_stripsControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		text string
		sent string
	}{
		{"paste の終わりの印と DEL と C1", "a\x1b[201~\nb\x7fc\u0085d", "\x1b[200~a[201~\nbcd\x1b[201~"},
		{"ESC と CR だけの 1 行", "\x1bOA\rx", "OAx"},
		{"タブを含む 1 行", "x\ty", "\x1b[200~x\ty\x1b[201~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			if _, err := f.runner().Run(context.Background(), request(tc.text)); err != nil {
				t.Fatal(err)
			}
			if got := f.host.SentTexts[0]; got != tc.sent {
				t.Fatalf("sent = %q, want %q", got, tc.sent)
			}
		})
	}
	// 制御文字しか無い文面は空として送らない。
	f := newFixture()
	if _, err := f.runner().Run(context.Background(), request("\x1b\x07\r\x7f")); err == nil || !strings.Contains(err.Error(), "空") || len(f.host.Calls) != 0 {
		t.Fatalf("err = %v, calls = %v", err, f.host.Calls)
	}
}

// 送らない経路。どれも SendText / SendKeys を呼ばない。
func TestRunner_Run_refuses(t *testing.T) {
	cases := []struct {
		name   string
		adjust func(f *fixture, req *Request)
		want   string
	}{
		{"session の block が無い", func(_ *fixture, req *Request) { req.ToSession = "no-such-session" }, "pane が見つからない"},
		{"claude が前景でない", func(f *fixture, _ *Request) { delete(f.panes, claudeID) }, "claude が前景"},
		{"prompt が開いている", func(f *fixture, _ *Request) {
			f.panes[claudeID] = multiplexer.Pane{PaneID: claudeID, Agent: "claude-code", AgentStatus: multiplexer.AgentStatusBlocked, AgentKind: multiplexer.AgentKindPermission, AgentMessage: "approve Bash?", SessionID: sid}
		}, "prompt"},
		// 上限かどうかは画面ではなく宛先のセッションの transcript で決める (#257)。
		{"宛先のセッションが上限で止まっている", func(f *fixture, _ *Request) { f.limited[sid] = "limit" }, "最後の turn が上限 (limit)"},
		{"本体の通知欄 (Enter 待ち) が出ている", func(f *fixture, _ *Request) { f.hints[idleScreen] = "resume-ready" }, "通知欄 (resume-ready)"},
		// 利用者が宛先の端末で打っていた文だと結び付くよう、どこの何かと、どうすれば送れるかを書く (#380)。
		{"入力欄に打ちかけの文", func(f *fixture, _ *Request) { f.screen, f.styled = typingScreen, typedStyled }, "宛先のセッションの端末の入力欄に、Enter を押していない打ちかけの文"},
		{"提案の後に打ちかけの文", func(f *fixture, _ *Request) {
			f.screen, f.styled = typingScreen, "❯ \x1b[0m\x1b[2mここまで\x1b[0m打った\r\n"
		}, "打ちかけの文 (3 文字) があるので送らない (pane " + claudeID + "。その端末で送るか消してから、もう一度押す)"},
		{"入力欄が見つからない", func(f *fixture, _ *Request) { f.screen = "何も無い画面\n" }, "入力欄"},
		{"文面が空", func(_ *fixture, req *Request) { req.Text = " \n" }, "空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			req := request("hello")
			tc.adjust(f, &req)
			_, err := f.runner().Run(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			// 相手の入力欄の中身は理由 (ログにも残る) に書かない。
			if strings.Contains(err.Error(), "ここまで打った") {
				t.Fatalf("打ちかけの文が理由に入っている: %v", err)
			}
			for _, c := range f.host.Calls {
				if strings.HasPrefix(c, "Send") {
					t.Fatalf("送っている: %v", f.host.Calls)
				}
			}
		})
	}
}

// 入力欄の残りが次の prompt の提案 (薄字) だけなら、打つと消えるので空とみなして送る。素の画面で残りがあるときだけ
// 制御シーケンス付きで読み直す (PaneRead が 2 回)。
func TestRunner_Run_ignoresSuggestion(t *testing.T) {
	f := newFixture()
	f.screen, f.styled = typingScreen, suggestionStyled
	if _, err := f.runner().Run(context.Background(), request("hello")); err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, c := range f.host.Calls {
		if strings.HasPrefix(c, "PaneRead") {
			reads++
		}
	}
	if reads != 2 || len(f.host.SentTexts) != 1 {
		t.Fatalf("reads = %d, sent = %v (calls %v)", reads, f.host.SentTexts, f.host.Calls)
	}
}

// 実機の vt の行 (rex capture --format=vt)。提案の行、打った行、送った prompt の echo (入力欄より上) の順。
func TestTypedRemainder(t *testing.T) {
	cases := []struct {
		styled string
		want   string
	}{
		{"❯ \x1b[0m\x1b[2mタスク「tell の検証」を TaskUpdate で completed にして\x1b[0m\r\n", ""},
		{"❯ x\r\n", "x"},
		{emptyStyled, ""},
		// 薄字の途中で色だけ変わっても薄字のまま。22 で薄字が終わる。
		{"❯ \x1b[2m提案\x1b[31m赤い提案\x1b[22m打った\x1b[0m\r\n", "打った"},
		// 入力欄より上の echo の行 (背景色付き) は見ない。
		{"❯ \x1b[0m\x1b[48;2;240;240;240m送った文\x1b[0m\r\n────\r\n❯ \x1b[2m提案\x1b[0m\r\n", ""},
		{"no prompt\r\n", ""},
	}
	for _, tc := range cases {
		if got := typedRemainder(tc.styled); got != tc.want {
			t.Errorf("typedRemainder(%q) = %q, want %q", tc.styled, got, tc.want)
		}
	}
}

// 文面を打ってから Enter までの間に prompt が開いたら、Enter を送らずに文面を入力欄に残して理由を返す (#372)。
// Enter が prompt の承認にならないようにする。pane を読み直せないときも Enter を送らない。
func TestRunner_Run_promptOpensBeforeEnter(t *testing.T) {
	cases := []struct {
		name   string
		during func(f *fixture)
		want   string
	}{
		{"permission の prompt が開いた", func(f *fixture) {
			f.panes[claudeID] = multiplexer.Pane{PaneID: claudeID, Agent: "claude-code", AgentStatus: multiplexer.AgentStatusBlocked, AgentKind: multiplexer.AgentKindPermission, AgentMessage: "approve Bash?", SessionID: sid}
		}, "prompt"},
		{"pane を読み直せない", func(f *fixture) { delete(f.panes, claudeID) }, "読み直せない"},
		// claude が終わって shell が前景になると、Enter で文面がコマンドとして走る。
		{"前景が shell に変わった", func(f *fixture) { f.panes[claudeID] = multiplexer.Pane{PaneID: claudeID, SessionID: sid} }, "claude が前景でない"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			r := f.runner()
			r.Sleep = func(time.Duration) { tc.during(f) }
			res, err := r.Run(context.Background(), request("hello"))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "文面は入力欄に残っている") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			// 文面を打った pane は、送れなかったときも記録 (event log) に残せるよう返す。
			if res.PaneID != claudeID {
				t.Fatalf("result = %+v", res)
			}
			if len(f.host.SentTexts) != 1 {
				t.Fatalf("sent = %v", f.host.SentTexts)
			}
			for _, c := range f.host.Calls {
				if strings.HasPrefix(c, "SendKeys") {
					t.Fatalf("Enter を送っている: %v", f.host.Calls)
				}
			}
		})
	}
}

// 文面を打てなければ Enter を送らずに失敗を返す。
func TestRunner_Run_sendTextFails(t *testing.T) {
	f := newFixture()
	f.host.SendTextFunc = func(context.Context, string, string) error { return errors.New("rex send: 落ちた") }
	_, err := f.runner().Run(context.Background(), request("hello"))
	if err == nil || !strings.Contains(err.Error(), "rex send: 落ちた") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.host.Calls {
		if strings.HasPrefix(c, "SendKeys") {
			t.Fatalf("Enter を送っている: %v", f.host.Calls)
		}
	}
}

func TestInputRemainder(t *testing.T) {
	cases := []struct {
		screen string
		rest   string
		found  bool
	}{
		{idleScreen, "", true},
		{typingScreen, "ここまで打った", true},
		{"❯ \n", "", true},
		{"  ❯  \n", "", true},
		// 入力欄より上に ❯ があっても、見るのは最後の行。
		{"❯ 古い行\n────\n❯ \n────\n", "", true},
		{"no prompt\n", "", false},
	}
	for _, tc := range cases {
		rest, found := inputRemainder(tc.screen)
		if rest != tc.rest || found != tc.found {
			t.Errorf("inputRemainder(%q) = %q, %v; want %q, %v", tc.screen, rest, found, tc.rest, tc.found)
		}
	}
}
