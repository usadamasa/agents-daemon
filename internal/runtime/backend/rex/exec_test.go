package rex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/apitest"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// fake の rex に渡す ID。session と Claude の block は 2026-10-10 の実測、閉じた block と shell 以外の block は作り物。
const (
	sessionA = "session:01a12439-0839-7829-a4cb-aeac453570c1"
	sessionB = "session:01a1250f-c100-71e0-aed0-4a433569cf00"

	blockClaudeWorking = "block:01a12439-083a-74f9-9827-49c3478b2469"
	blockClaudeDone    = "block:01a12540-cd47-7943-a503-91a06c58fab5"
	blockClosed        = "block:01a12599-0000-7000-8000-000000000001"
	blockNotShell      = "block:01a12599-0000-7000-8000-000000000002"
	blockClosedMidway  = "block:01a12599-0000-7000-8000-000000000003"

	windowA = "window:01a1f8fb-31ab-7af4-a1c6-699a246f65ab"

	sidWorking = "0d5e717f-82c5-4e71-ac74-799f7197e0de"
	sidDone    = "6b1f3c2a-1111-4222-8333-944455556666"
)

func staticResolver(m map[string]string) SessionResolver {
	return func() (map[string]string, error) { return m, nil }
}

// TestExecClient_PaneList は 2 session を回る 1 回の PaneList の配線を見る。session A に作業中の Claude と
// shell 以外の block、session B に list_blocks の後に閉じた block、program_status と process の間に閉じた block、
// 入力待ちの Claude を置く。
func TestExecClient_PaneList(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA,
		[2]string{blockClaudeWorking, wire.ShellFlavor},
		[2]string{blockNotShell, "com.superlogical.browser"},
	)
	f.AddSession(sessionB,
		[2]string{blockClosed, wire.ShellFlavor},
		[2]string{blockClosedMidway, wire.ShellFlavor},
		[2]string{blockClaudeDone, wire.ShellFlavor},
	)
	f.PutBlock(blockClaudeWorking, measured(t, measuredOct10, "program_status-working.json"), processJSON(pidClaudeWorking, "claude", "/w/agents-daemon"))
	f.Put("program_status", blockClosedMidway, psEmpty)
	f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))
	f.SetView(sessionA, `{"revision":4,"session_id":"`+sessionA+`","label":"work","windows":[{"window_id":"`+windowA+`","label":"peer-a",`+
		`"layers":[{"kind":"tiled","blocks":[{"block_id":"`+blockClaudeWorking+`"},{"block_id":"`+blockNotShell+`"}]}]}]}`)

	resolves := 0
	c := f.client(func() (map[string]string, error) {
		resolves++
		return map[string]string{blockClaudeWorking: sidWorking, blockClaudeDone: sidDone}, nil
	})
	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v (閉じた block は読み飛ばすべき)", err)
	}

	t.Run("閉じた block を飛ばし session_id と session と window を埋める", func(t *testing.T) {
		// msg は fixture の実測値 (作業中の要約と、ターン終了の要約)。session B の view は window を持たない。
		want := []multiplexer.Pane{
			{PaneID: blockClaudeWorking, CWD: "/w/agents-daemon", PID: pidClaudeWorking, Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking, AgentMessage: "Showing rex CLI help for relevant subcommands", SessionID: sidWorking,
				Location: multiplexer.Location{SessionID: sessionA, SessionLabel: "work", WindowID: windowA, WindowLabel: "peer-a"}},
			{PaneID: blockClaudeDone, CWD: "/w/guard", PID: pidClaudeDone, Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "guard-gql-read の PR 作成待ち", SessionID: sidDone,
				Location: multiplexer.Location{SessionID: sessionB, SessionLabel: apitest.DefaultSessionLabel}},
		}
		if !slices.Equal(panes, want) {
			t.Errorf("PaneList() =\n%+v\nwant\n%+v", panes, want)
		}
	})
	t.Run("shell の block にだけ method を打ち、session ごとに view を 1 回読む", func(t *testing.T) {
		f.assertCalls(t,
			"session.list",
			"session.list_blocks "+sessionA,
			"session.view "+sessionA,
			"program_status "+blockClaudeWorking,
			"process "+blockClaudeWorking,
			"session.list_blocks "+sessionB,
			"session.view "+sessionB,
			"program_status "+blockClosed,
			"program_status "+blockClosedMidway,
			"process "+blockClosedMidway,
			"program_status "+blockClaudeDone,
			"process "+blockClaudeDone,
		)
	})
	t.Run("block の method は block の session を名指す", func(t *testing.T) {
		for _, p := range f.RequestsOf(apitest.TerminalMethod + "process") {
			var req struct {
				SessionID string `json:"session_id"`
				BlockID   string `json:"block_id"`
			}
			if err := json.Unmarshal(p, &req); err != nil {
				t.Fatal(err)
			}
			want := sessionB
			if req.BlockID == blockClaudeWorking {
				want = sessionA
			}
			if req.SessionID != want {
				t.Errorf("%s の process の session_id = %q, want %q", req.BlockID, req.SessionID, want)
			}
		}
	})
	t.Run("resolve は 1 回", func(t *testing.T) {
		if resolves != 1 {
			t.Errorf("resolve の呼び出し回数 = %d, want 1", resolves)
		}
	})
}

// list の後に閉じた session (list_blocks が session_not_found) を飛ばし、残りの session へ進む。
func TestExecClient_PaneList_SkipsClosedSession(t *testing.T) {
	f := newFakeAPI(t)
	f.ListOnly(sessionA)
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))

	panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v (閉じた session は読み飛ばすべき)", err)
	}
	if len(panes) != 1 || panes[0].PaneID != blockClaudeDone {
		t.Errorf("PaneList() = %+v, want %s の 1 件", panes, blockClaudeDone)
	}
	f.assertCalls(t,
		"session.list",
		"session.list_blocks "+sessionA,
		"session.list_blocks "+sessionB,
		"session.view "+sessionB,
		"program_status "+blockClaudeDone,
		"process "+blockClaudeDone,
	)
}

// view の読み取りが失敗しても pane の一覧は返す。置き場は表示にしか使わないので、view を読めない session の pane は
// 置き場を空にする (session の置き場の読み取りで daemon の PaneList を失敗に数えない)。
func TestExecClient_PaneList_ViewFailureLeavesLocationEmpty(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))
	f.Fail("session.view", sessionB)

	panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
	if err != nil || len(panes) != 1 {
		t.Fatalf("PaneList() = %+v, %v; want 1 件", panes, err)
	}
	if panes[0].Location != (multiplexer.Location{}) {
		t.Errorf("Location = %+v, want 空", panes[0].Location)
	}
}

// view を読めない理由 (server の失敗、応答の形の崩れ) は pane の LocationReason に載り、置き場が空なこと (window に
// 置かれていない block) と区別できる。キーの欠落は型付きの decode では失敗しないので、欠けたら形の崩れとして扱う。
func TestExecClient_PaneList_ViewFailureReason(t *testing.T) {
	window := func(layers string) string {
		return `{"revision":4,"session_id":"` + sessionB + `","label":"work","windows":[{"window_id":"` + windowA + `","label":"w",` + layers + `}]}`
	}
	tests := []struct {
		name   string
		setup  func(f *fakeAPI)
		reason string
	}{
		{"server の失敗", func(f *fakeAPI) { f.Fail("session.view", sessionB) }, "server error"},
		{"windows の欠落", func(f *fakeAPI) {
			f.SetView(sessionB, `{"revision":4,"session_id":"`+sessionB+`","label":"work"}`)
		}, "windows"},
		{"session_id の欠落", func(f *fakeAPI) {
			f.SetView(sessionB, `{"revision":4,"label":"work","windows":[]}`)
		}, "session_id"},
		{"layers の改名", func(f *fakeAPI) {
			f.SetView(sessionB, window(`"panes":[{"blocks":[{"block_id":"`+blockClaudeDone+`"}]}]`))
		}, "layers"},
		{"blocks の欠落", func(f *fakeAPI) { f.SetView(sessionB, window(`"layers":[{"kind":"tiled"}]`)) }, "blocks"},
		{"block_id の欠落", func(f *fakeAPI) { f.SetView(sessionB, window(`"layers":[{"blocks":[{"id":"x"}]}]`)) }, "block_id"},
		{"window_id の欠落", func(f *fakeAPI) {
			f.SetView(sessionB, `{"revision":4,"session_id":"`+sessionB+`","label":"work","windows":[{"label":"w","layers":[]}]}`)
		}, "window_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
			f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))
			tt.setup(f)

			panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
			if err != nil || len(panes) != 1 {
				t.Fatalf("PaneList() = %+v, %v; want 1 件", panes, err)
			}
			if panes[0].Location != (multiplexer.Location{}) {
				t.Errorf("Location = %+v, want 空", panes[0].Location)
			}
			if !strings.Contains(panes[0].LocationReason, tt.reason) {
				t.Errorf("LocationReason = %q, want %q を含む", panes[0].LocationReason, tt.reason)
			}
		})
	}
}

// window に置かれていない block (detached) は置き場の session だけを持ち、読めなかった理由は空。
func TestExecClient_PaneList_DetachedHasNoReason(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))

	panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
	if err != nil || len(panes) != 1 {
		t.Fatalf("PaneList() = %+v, %v; want 1 件", panes, err)
	}
	if panes[0].LocationReason != "" || panes[0].Location.SessionID != sessionB {
		t.Errorf("Location = %+v、LocationReason = %q; want session だけを持ち理由は空", panes[0].Location, panes[0].LocationReason)
	}
}

// not found 以外の失敗は block を飛ばさずエラーにする。
func TestExecClient_PaneList_FailureIsError(t *testing.T) {
	oneBlock := func(f *fakeAPI, programStatus string) {
		f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
		f.Put("program_status", blockClaudeWorking, programStatus)
	}
	tests := []struct {
		name    string
		setup   func(f *fakeAPI)
		wantErr string
	}{
		{"list_blocks が not found 以外で失敗", func(f *fakeAPI) {
			f.AddSession(sessionA)
			f.Fail("session.list_blocks", sessionA)
		}, "server error"},
		{"block の method が not found 以外で失敗", func(f *fakeAPI) {
			oneBlock(f, psEmpty)
			f.Fail("program_status", blockClaudeWorking)
		}, "server error"},
		{"program_status が records の形でない", func(f *fakeAPI) { oneBlock(f, `"not records"`) }, "応答の parse に失敗"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			_, err := f.client(staticResolver(nil)).PaneList(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PaneList() error = %v, want %q を含む", err, tt.wantErr)
			}
		})
	}
	t.Run("server に繋がらない", func(t *testing.T) {
		c := &execClient{resolve: staticResolver(nil), rex: api.New(fixedSocket(t.TempDir()+"/none.sock"), apiTimeout)}
		if _, err := c.PaneList(context.Background()); err == nil {
			t.Fatal("繋がらないのに PaneList が成功した")
		}
	})
}

// PaneGet は block の session を target.resolve で引いてから block の method を打つ (block の method は session_id が要る)。
func TestExecClient_PaneGet(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w"))
	c := f.client(staticResolver(map[string]string{blockClaudeDone: sidDone}))

	got, err := c.PaneGet(context.Background(), blockClaudeDone)
	if err != nil {
		t.Fatalf("PaneGet() error = %v", err)
	}
	want := multiplexer.Pane{PaneID: blockClaudeDone, CWD: "/w", PID: pidClaudeDone, Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "guard-gql-read の PR 作成待ち", SessionID: sidDone}
	if got != want {
		t.Errorf("PaneGet() = %+v, want %+v", got, want)
	}
	f.assertCalls(t,
		"target.resolve "+blockClaudeDone,
		"program_status "+blockClaudeDone,
		"process "+blockClaudeDone,
	)
	// 文脈を付けずに全 session から引く (文脈の session に閉じると別 session の block が not_found になる)。
	var req struct {
		Kinds   []string        `json:"kinds"`
		Context json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(f.RequestsOf("target.resolve")[0], &req); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(req.Kinds, []string{"block"}) || req.Context != nil {
		t.Errorf("target.resolve の kinds = %v, context = %s", req.Kinds, req.Context)
	}
}

// block を相手にする method は、block が無ければ not found の error を返す (target.resolve が見つけられない)。
func TestExecClient_ReadTargetNotFoundIsError(t *testing.T) {
	tests := []struct {
		name string
		call func(c *execClient) error
	}{
		{"PaneGet", func(c *execClient) error {
			_, err := c.PaneGet(context.Background(), blockClosed)
			return err
		}},
		{"PaneRead", func(c *execClient) error {
			_, err := c.PaneRead(context.Background(), blockClosed, multiplexer.PaneReadOptions{})
			return err
		}},
		{"PaneGeometry", func(c *execClient) error {
			_, err := c.PaneGeometry(context.Background(), blockClosed)
			return err
		}},
		{"SendText", func(c *execClient) error { return c.SendText(context.Background(), blockClosed, "続けて") }},
		{"SendKeys", func(c *execClient) error { return c.SendKeys(context.Background(), blockClosed, "enter") }},
		{"ClosePane", func(c *execClient) error { return c.ClosePane(context.Background(), blockClosed) }},
		{"SplitPane", func(c *execClient) error {
			_, err := c.SplitPane(context.Background(), blockClosed, "x", []string{"true"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(newFakeAPI(t).client(staticResolver(nil)))
			if err == nil || !strings.Contains(err.Error(), "not found") || !isNotFound(err) {
				t.Errorf("error = %v, want not found", err)
			}
		})
	}
}

// resolve の後に閉じた block は block の method が not_found で返し、not found の error になる。
func TestExecClient_PaneGet_ClosedAfterResolve(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionB, [2]string{blockClosedMidway, wire.ShellFlavor})
	_, err := f.client(staticResolver(nil)).PaneGet(context.Background(), blockClosedMidway)
	if !isNotFound(err) {
		t.Errorf("PaneGet() error = %v, want not found", err)
	}
}

// PaneRead は format の content が改行で終わっていなければ改行を 1 つ足す。rex の CLI の capture は出力を必ず
// 改行で終え (help の Output always ends with a newline)、末尾の n 行を切り出す tailLines と呼び手はその形を
// 前提にしている。trim:true の content は改行で終わらないので、実機では capture は content と改行 1 つに
// バイト単位で一致する (2026-11-17 に text と vt で比べた)。
func TestExecClient_PaneRead(t *testing.T) {
	const content = "line1\nline2\nline3\nline4"
	tests := []struct {
		name    string
		content string
		lines   int
		want    string
	}{
		{"Lines 0 は全体に改行を足す", content, 0, content + "\n"},
		{"末尾 2 行", content, 2, "line3\nline4\n"},
		{"行数より多ければ全体", content, 10, content + "\n"},
		{"空の画面は改行 1 つ", "", 0, "\n"},
		{"改行で終わっていれば足さない", content + "\n", 0, content + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
			f.Put("format", blockClaudeWorking, tt.content)
			got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking,
				multiplexer.PaneReadOptions{Lines: tt.lines})
			if err != nil {
				t.Fatalf("PaneRead() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PaneRead() = %q, want %q", got, tt.want)
			}
			f.assertCalls(t, "target.resolve "+blockClaudeWorking, "format "+blockClaudeWorking)
			assertFormatArgs(t, f, `{"trim":true,"unwrap":true,"max_bytes":983040}`)
		})
	}
}

// 1 回の応答の上限 (1 MiB) を超える画面は、format の max_bytes で先頭を受け、続きを blob_read で読み足す。
// max_bytes を付けずに頼むと too_large で落ちる (2026-11-17 に 3 MB の scrollback で実測。CLI の capture は全体を返した)。
func TestExecClient_PaneRead_LargeScreen(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	line := strings.Repeat("x", 99) + "\n"
	content := strings.Repeat(line, 25000) + "last" // 約 2.5 MB
	f.Put("format", blockClaudeWorking, content)

	got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking, multiplexer.PaneReadOptions{})
	if err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if got != content+"\n" {
		t.Errorf("PaneRead() の長さ = %d, want %d (全体に改行 1 つ)", len(got), len(content)+1)
	}
	if calls := f.Calls(); len(calls) < 3 || calls[1] != "format "+blockClaudeWorking || calls[2] != "blob_read "+blockClaudeWorking {
		t.Errorf("calls = %v, want target.resolve、format、blob_read の続き", calls)
	}
	// 続きは format が返した id から、受け取った分の後ろを読む。fake の blob_read は max_bytes の半分しか返さない
	// (実機も max_bytes より短く返しうる) ので、offset は受けた data の累計で進む。
	for i, p := range f.RequestsOf(apitest.TerminalMethod + "blob_read") {
		var req struct {
			Args struct {
				ID       int `json:"id"`
				Offset   int `json:"offset"`
				MaxBytes int `json:"max_bytes"`
			} `json:"args"`
		}
		if err := json.Unmarshal(p, &req); err != nil {
			t.Fatal(err)
		}
		if want := 983040 + i*983040/2; req.Args.ID != apitest.BlobID || req.Args.Offset != want || req.Args.MaxBytes != 983040 {
			t.Errorf("%d 回目の blob_read の args = %+v, want id %d offset %d", i+1, req.Args, apitest.BlobID, want)
		}
	}
}

// 続きが残っているのに blob_read が空の data を返したら、途中までの画面を返さずに error にする。
func TestExecClient_PaneRead_TruncatedBlobIsError(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	f.Put("format", blockClaudeWorking, strings.Repeat("x", 2*983040))
	f.TruncateBlob(blockClaudeWorking)
	if got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking, multiplexer.PaneReadOptions{}); err == nil {
		t.Errorf("PaneRead() = %d バイト, want error (total に届かない)", len(got))
	}
}

// 閉じかけの session (list_blocks が conflict) は、閉じた session と同じく全列挙で読み飛ばす。
func TestExecClient_PaneList_SkipsClosingSession(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	f.PutBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w"))
	f.Closing(sessionA)
	panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
	if err != nil || len(panes) != 1 || panes[0].PaneID != blockClaudeDone {
		t.Errorf("PaneList() = %+v, %v; want 閉じかけの session を飛ばした 1 件", panes, err)
	}
}

// assertFormatArgs は最後の format の args を want (JSON) と比べる。trim と unwrap は CLI の capture --trim --unwrap と
// 同じ。max_bytes は 1 回の応答の上限に収まる大きさ (server が受ける最大の 960 KiB)。
func assertFormatArgs(t *testing.T, f *fakeAPI, want string) {
	t.Helper()
	reqs := f.RequestsOf(apitest.TerminalMethod + "format")
	var req struct {
		SessionID string          `json:"session_id"`
		Args      json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(reqs[len(reqs)-1], &req); err != nil {
		t.Fatal(err)
	}
	var got, w any
	if err := json.Unmarshal(req.Args, &got); err != nil {
		t.Fatalf("args を読めない: %s", req.Args)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("format の args = %s, want %s", req.Args, want)
	}
	if req.SessionID != sessionA {
		t.Errorf("format の session_id = %q, want %q", req.SessionID, sessionA)
	}
}

// Styled は制御シーケンスを保ったまま読む (format: vt)。末尾の改行は text と同じく足す。
func TestExecClient_PaneRead_styled(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	styled := "❯ \x1b[2mhint\x1b[0m  "
	f.Put("format", blockClaudeWorking, styled)
	got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking,
		multiplexer.PaneReadOptions{Styled: true})
	if err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if got != "❯ \x1b[2mhint\x1b[0m  \n" {
		t.Errorf("PaneRead() = %q", got)
	}
	assertFormatArgs(t, f, `{"trim":true,"unwrap":true,"format":"vt","max_bytes":983040}`)
}

// PaneGeometry は size と modes の 2 回で block の高さと screen を返す。screen の未知の値は primary と見る
// (modes の output_schema: Treat any other value as 'primary')。
func TestExecClient_PaneGeometry(t *testing.T) {
	tests := []struct {
		name  string
		modes string
		want  multiplexer.Geometry
	}{
		{"実測の alternate", measured(t, measuredOct20, "modes.json"), multiplexer.Geometry{Rows: 52, Columns: 139, AlternateScreen: true}},
		{"primary", `{"screen":"primary","modes":[]}`, multiplexer.Geometry{Rows: 52, Columns: 139}},
		{"未知の値は primary", `{"screen":"weird","modes":null}`, multiplexer.Geometry{Rows: 52, Columns: 139}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
			f.Put("size", blockClaudeWorking, measured(t, measuredOct20, "size.json"))
			f.Put("modes", blockClaudeWorking, tt.modes)
			got, err := f.client(staticResolver(nil)).PaneGeometry(context.Background(), blockClaudeWorking)
			if err != nil {
				t.Fatalf("PaneGeometry() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PaneGeometry() = %+v, want %+v", got, tt.want)
			}
			f.assertCalls(t,
				"target.resolve "+blockClaudeWorking,
				"size "+blockClaudeWorking,
				"modes "+blockClaudeWorking,
			)
		})
	}
}

// SendText は文面を write の data にそのまま渡し、SendKeys は key ごとに block.event の press と release を送る
// (CLI の send-key と同じ。2026-11-17 に実機で cat -v に届く文字が CLI と同じことを確かめた)。
func TestExecClient_SendTextAndKeys(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	c := f.client(staticResolver(nil))

	if err := c.SendText(context.Background(), blockClaudeDone, "続けて ください"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := c.SendKeys(context.Background(), blockClaudeDone, "esc", "enter"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	f.assertCalls(t,
		"target.resolve "+blockClaudeDone,
		"write "+blockClaudeDone,
		"target.resolve "+blockClaudeDone,
		"block.event "+blockClaudeDone,
		"block.event "+blockClaudeDone,
		"block.event "+blockClaudeDone,
		"block.event "+blockClaudeDone,
	)
	var write struct {
		SessionID string `json:"session_id"`
		Args      struct {
			Data string `json:"data"`
		} `json:"args"`
	}
	f.payloadOf(t, apitest.TerminalMethod+"write", &write)
	if write.SessionID != sessionB || write.Args.Data != "続けて ください" {
		t.Errorf("write の payload = %+v", write)
	}
	want := []string{
		`{"key":"escape","mods":"","consumed_mods":"","text":"","unshifted_codepoint":27,"composing":false,"action":"press"}`,
		`{"key":"escape","mods":"","consumed_mods":"","text":"","unshifted_codepoint":27,"composing":false,"action":"release"}`,
		`{"key":"enter","mods":"","consumed_mods":"","text":"","unshifted_codepoint":13,"composing":false,"action":"press"}`,
		`{"key":"enter","mods":"","consumed_mods":"","text":"","unshifted_codepoint":13,"composing":false,"action":"release"}`,
	}
	for i, p := range f.RequestsOf("block.event") {
		var ev struct {
			SessionID string `json:"session_id"`
			Event     struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			} `json:"event"`
		}
		if err := json.Unmarshal(p, &ev); err != nil {
			t.Fatal(err)
		}
		var got, w any
		_ = json.Unmarshal(ev.Event.Payload, &got)
		_ = json.Unmarshal([]byte(want[i]), &w)
		if ev.SessionID != sessionB || ev.Event.Type != "key" || !reflect.DeepEqual(got, w) {
			t.Errorf("%d 番目の block.event = %s, want key %s", i, p, want[i])
		}
	}
}

// 知らない key の名前は何も送らずに error にする (CLI の key binding の構文は持たない。daemon が送るのは esc と enter だけ)。
func TestExecClient_SendKeys_UnknownKey(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	err := f.client(staticResolver(nil)).SendKeys(context.Background(), blockClaudeDone, "enter", "ctrl+c")
	if err == nil || !strings.Contains(err.Error(), "ctrl+c") {
		t.Fatalf("SendKeys() error = %v, want 知らない key の error", err)
	}
	if got := f.RequestsOf("block.event"); len(got) != 0 {
		t.Errorf("知らない key を含むのに送った: %s", got)
	}
}

func TestExecClient_Reachable(t *testing.T) {
	tests := []struct {
		name    string
		status  string // 空なら server.status が失敗する
		wantErr bool
	}{
		{"pid があれば到達", measured(t, measuredOct10, "server_status.json"), false},
		{"pid 0 は到達でない", `{"pid":0}`, true},
		{"pid が数でない", `{"pid":"x"}`, true},
		{"server.status が失敗", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.SetStatus(tt.status)
			err := f.client(staticResolver(nil)).Reachable(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Reachable() error = %v, wantErr %v", err, tt.wantErr)
			}
			f.assertCalls(t, "server.status")
		})
	}
	t.Run("server に繋がらない", func(t *testing.T) {
		c := &execClient{resolve: staticResolver(nil), rex: api.New(fixedSocket(t.TempDir()+"/none.sock"), apiTimeout)}
		if err := c.Reachable(context.Background()); err == nil {
			t.Fatal("繋がらないのに Reachable が成功した")
		}
	})
}

// 接続は 1 本を使い回し、張った直後に session.attach_all を打つ (attach していないと session を名指す method が
// conflict になる)。購読はまだ CLI なので、event は block_event を含まない session の範囲に絞る。
func TestExecClient_ReusesOneConnection(t *testing.T) {
	f := newFakeAPI(t)
	f.SetStatus(`{"pid":1}`)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	f.Put("format", blockClaudeWorking, "x")
	c := f.client(staticResolver(nil))
	for range 3 {
		if err := c.Reachable(context.Background()); err != nil {
			t.Fatalf("Reachable() error = %v", err)
		}
	}
	if _, err := c.PaneRead(context.Background(), blockClaudeWorking, multiplexer.PaneReadOptions{}); err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if n := len(f.Protocol().Hellos()); n != 1 {
		t.Errorf("接続を %d 回張った (1 回のはず)", n)
	}
	attaches := f.RequestsOf("session.attach_all")
	if len(attaches) != 1 || string(attaches[0]) != `{"event_scope":"session"}` {
		t.Errorf("attach_all = %s", attaches)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	eventually(t, "server の接続が閉じる", func() bool { return f.Protocol().Connections() == 0 })
}

// server が切れたら次の呼び出しで張り直す (rex の server の再起動、更新)。
func TestExecClient_RedialsAfterDisconnect(t *testing.T) {
	f := newFakeAPI(t)
	f.SetStatus(`{"pid":1}`)
	c := f.client(staticResolver(nil))
	if err := c.Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable() error = %v", err)
	}
	f.Protocol().CloseConnections()
	eventually(t, "server の接続が閉じる", func() bool { return f.Protocol().Connections() == 0 })
	eventually(t, "張り直した Reachable が通る", func() bool { return c.Reachable(context.Background()) == nil })
	if n := len(f.Protocol().Hellos()); n != 2 {
		t.Errorf("接続を %d 回張った (2 回のはず)", n)
	}
}

// 応答が返らない request は Client の timeout で打ち切る。
func TestExecClient_APITimeout(t *testing.T) {
	f := newFakeAPI(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f.Protocol().Handle("server.status", func(json.RawMessage) (any, error) { <-block; return nil, nil })
	c := f.client(staticResolver(nil))
	c.rex = api.New(fixedSocket(f.Socket()), 50*time.Millisecond)
	start := time.Now()
	err := c.Reachable(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Reachable() error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed = %v, want タイムアウトで打ち切る", elapsed)
	}
}

// NewExecClient が使う socket は REX_SERVER から引く。rex の外 (REX_SERVER が無い) では既定の場所を使う。
func TestDefaultSocket(t *testing.T) {
	t.Setenv("REX_SERVER", "unix:///tmp/x%20y/server.sock")
	got, err := api.DefaultSocket()
	if err != nil || got != "/tmp/x y/server.sock" {
		t.Errorf("DefaultSocket() = %q, %v", got, err)
	}
}
