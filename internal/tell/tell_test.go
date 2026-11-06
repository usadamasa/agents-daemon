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

type fixture struct {
	host   *multiplexerfake.Client
	panes  map[string]multiplexer.Pane
	screen string
	kinds  map[string]string
	slept  []time.Duration
}

func newFixture() *fixture {
	f := &fixture{
		panes: map[string]multiplexer.Pane{
			claudeID: {PaneID: claudeID, Agent: "claude-code", AgentStatus: multiplexer.AgentStatusIdle, SessionID: sid},
			shellID:  {PaneID: shellID, SessionID: sid},
			otherID:  {PaneID: otherID, Agent: "claude-code", AgentStatus: multiplexer.AgentStatusWorking, SessionID: otherSID},
		},
		screen: idleScreen,
		kinds:  map[string]string{},
	}
	f.host = &multiplexerfake.Client{
		PaneGetFunc: func(_ context.Context, id string) (multiplexer.Pane, error) {
			p, ok := f.panes[id]
			if !ok {
				return multiplexer.Pane{}, errors.New("block が無い")
			}
			return p, nil
		},
		PaneReadFunc: func(_ context.Context, _ string, _ multiplexer.PaneReadOptions) (string, error) { return f.screen, nil },
	}
	return f
}

func (f *fixture) runner() Runner {
	return Runner{
		Host: f.host,
		Blocks: func() (map[string]string, error) {
			return map[string]string{claudeID: sid, shellID: sid, otherID: otherSID}, nil
		},
		Screen:      func(text string) string { return f.kinds[text] },
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
	if res.PaneID != claudeID || res.Warning != "" {
		t.Fatalf("result = %+v", res)
	}
	// block は ID の順に見る (shellID の方が古い)。
	want := []string{"PaneGet:" + shellID, "PaneGet:" + claudeID, "PaneRead:" + claudeID, "SendText:" + claudeID, "SendKeys:" + claudeID}
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

// 文面が上限の検知語に当たるときは送ったうえで注意を返す。
func TestRunner_Run_warnsOnDetectionWords(t *testing.T) {
	f := newFixture()
	text := "rate limit の画面の文言を直して"
	f.kinds[text] = "limit"
	res, err := f.runner().Run(context.Background(), request(text))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warning, "limit") || len(f.host.SentTexts) != 1 {
		t.Fatalf("result = %+v, sent = %v", res, f.host.SentTexts)
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
		{"上限の画面", func(f *fixture, _ *Request) { f.kinds[idleScreen] = "limit" }, "上限"},
		{"入力欄に打ちかけの文", func(f *fixture, _ *Request) { f.screen = typingScreen }, "打ちかけ"},
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
