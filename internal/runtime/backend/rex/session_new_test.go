package rex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control/controltest"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wrap"
)

// spawned の session と block は session.create の応答。形は 2026-11-17 の実測。
const (
	spawnedSession = "session:01a1b37a-0560-743d-adfd-959e5e1c53a9"
	spawnedBlock   = "block:01a1b37a-0560-74ce-af88-d37d23de33b9"
	spawnedWindow  = "window:01a1b37a-056e-70fa-90fd-15d50af005a9"
	sessionNewJSON = `{"session_id":"` + spawnedSession + `","initial_windows":[{"window_id":"` + spawnedWindow + `","block_ids":["` + spawnedBlock + `"],"revision":1}]}`
)

// useFakeSelf は包みが印を書くのに呼ぶ agents-daemon を、引数を self.log へ 1 行ずつ書くだけの script に差し替え、
// 包みが叩く rex を fake の rex にする。
func useFakeSelf(t *testing.T, f *fakeRex) string {
	t.Helper()
	path := filepath.Join(f.dir, "agents-daemon")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s ' \"$a\"; done >> " + shellQuote(filepath.Join(f.dir, "self.log")) +
		"\necho >> " + shellQuote(filepath.Join(f.dir, "self.log")) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { // #nosec G306 -- テストの実行ファイル
		t.Fatal(err)
	}
	savedSelf, savedRex := selfBinary, rexBinary
	selfBinary = func() (string, error) { return path, nil }
	rexBinary = func() (string, error) { return f.bin, nil }
	t.Cleanup(func() { selfBinary, rexBinary = savedSelf, savedRex })
	return path
}

// spawnAPI は session.create が spawned の session を返し、その session に attach と disown が通る fake の API を返す。
func spawnAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.Protocol().Reply("session.create", json.RawMessage(sessionNewJSON))
	f.AddUnlisted(spawnedSession, [2]string{spawnedBlock, wire.ShellFlavor})
	return f
}

// blockOptions は作った block の flavor と options。
type blockOptions struct {
	Flavor  string `json:"flavor"`
	Label   string `json:"label"`
	Options struct {
		CWD     string   `json:"cwd"`
		Command []string `json:"command"`
		Shell   *string  `json:"shell"`
		Login   *bool    `json:"login"`
		Exit    *struct {
			OnCompletion bool `json:"on_completion"`
		} `json:"exit"`
	} `json:"options"`
}

// createRequest は session.create の payload。
type createRequest struct {
	Label          *string `json:"label"`
	InitialWindows []struct {
		WindowLabel *string `json:"window_label"`
		Layout      struct {
			Block blockOptions `json:"block"`
		} `json:"layout"`
	} `json:"initial_windows"`
}

// TestExecClient_NewSession は新しい rex session でコマンドを起こす配線を見る。argv は wrap の包みの後にそのまま command へ
// 渡す (空白や `$` を含む引数も 1 つのまま。実機で確かめた)。CLI の session new と同じく shell と login は既定のまま
// (login shell の環境で起こす) にし、失敗の画面は包みが Enter まで残すので exit も既定のままにする。
// 作った block は CLI と同じく、作った client の持ち物にしない (session.attach の後に disown_block で手放す)。
func TestExecClient_NewSession(t *testing.T) {
	f := spawnAPI(t)
	rex := newFakeRex(t)
	self := useFakeSelf(t, rex)
	c := f.client(staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	argv := []string{"claude", "--session-id", sidDone, "--", prompt}
	got, err := c.NewSession(context.Background(), "example-repo", "/w/example-repo", argv)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if want := (multiplexer.Placement{SessionID: spawnedSession, WindowID: spawnedWindow, PaneID: spawnedBlock}); got != want {
		t.Errorf("NewSession() = %+v, want %+v", got, want)
	}
	// 手放した後は session.detach で attach を外す。attach したままだと、その session の block_event がこの呼び出しの
	// 接続 (event_scope session) に流れ続ける。
	f.assertCalls(t, "session.create", "session.attach "+spawnedSession, "session.disown_block "+spawnedSession, "session.detach "+spawnedSession)

	var req createRequest
	f.payloadOf(t, "session.create", &req)
	if req.Label == nil || *req.Label != "example-repo" || len(req.InitialWindows) != 1 ||
		req.InitialWindows[0].WindowLabel == nil || *req.InitialWindows[0].WindowLabel != "example-repo" {
		t.Errorf("session.create の label と window_label = %+v", req)
	}
	block := req.InitialWindows[0].Layout.Block
	wantCommand := wrap.SessionCommand(rex.bin, self, argv)
	if block.Flavor != wire.ShellFlavor || block.Options.CWD != "/w/example-repo" || !slices.Equal(block.Options.Command, wantCommand) {
		t.Errorf("block = %+v\nwant command %q", block, wantCommand)
	}
	if block.Options.Shell != nil || block.Options.Login != nil || block.Options.Exit != nil {
		t.Errorf("shell / login / exit を既定から変えた: %+v", block.Options)
	}
	var disown struct {
		BlockIDs []string `json:"block_ids"`
	}
	f.payloadOf(t, "session.disown_block", &disown)
	if !slices.Equal(disown.BlockIDs, []string{spawnedBlock}) {
		t.Errorf("disown_block の block_ids = %q", disown.BlockIDs)
	}
}

// 作った block を手放せなくても (disown_block の失敗)、block は動いているので作成は成功にし、attach は外す。
func TestExecClient_NewSession_ReleaseFailureStillSucceeds(t *testing.T) {
	f := spawnAPI(t)
	useFakeSelf(t, newFakeRex(t))
	f.Protocol().Fail("session.disown_block", &controltest.Error{Code: "internal", Message: "boom", Status: 500})
	got, err := f.client(staticResolver(nil)).NewSession(context.Background(), "x", "/w/x", []string{"claude"})
	if err != nil || got.PaneID != spawnedBlock {
		t.Fatalf("NewSession() = %+v, %v; want 作った block", got, err)
	}
	f.assertCalls(t, "session.create", "session.attach "+spawnedSession, "session.disown_block "+spawnedSession, "session.detach "+spawnedSession)
}

// label は session と window の名前。先頭の `-` は外し (CLI が flag と読んで拒んだ名残で、名前を揃える)、
// 外して空なら label を付けない (rex の既定の名前になる)。
func TestExecClient_NewSession_DashLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string // 空なら label と window_label を送らない
	}{
		{label: "-x", want: "x"},
		{label: "--", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			f := spawnAPI(t)
			rex := newFakeRex(t)
			useFakeSelf(t, rex)
			if _, err := f.client(staticResolver(nil)).NewSession(context.Background(), tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			var req createRequest
			f.payloadOf(t, "session.create", &req)
			if !labelIs(req.Label, tt.want) || !labelIs(req.InitialWindows[0].WindowLabel, tt.want) {
				t.Errorf("label = %v, window_label = %v, want %q", req.Label, req.InitialWindows[0].WindowLabel, tt.want)
			}
		})
	}
}

// labelIs は送った label が want か (want が空なら送っていない) を返す。
func labelIs(got *string, want string) bool {
	if want == "" {
		return got == nil
	}
	return got != nil && *got == want
}

// spawnedArgv は session.create に渡した command を返す。rex が block の中で起こす argv そのもの。
func spawnedArgv(t *testing.T, f *fakeAPI) []string {
	t.Helper()
	var req createRequest
	f.payloadOf(t, "session.create", &req)
	return req.InitialWindows[0].Layout.Block.Options.Command
}

// runWrapped は argv を env で起こし、異常終了なら Enter (interrupt なら SIGINT) で待ちを抜けさせて終わりを待つ。
// 0 以外で終わったときは、Enter の前に終わらないことと、待つ間に rex を呼ばないことを見る。
func runWrapped(t *testing.T, argv []string, env []string, exit int, interrupt bool, rex *fakeRex) (stdout string, err error) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- テストが組んだ argv
	cmd.Env = append(os.Environ(), env...)
	var out strings.Builder
	cmd.Stdout = &out
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("包んだ argv の起動 error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if exit != 0 {
		select {
		case err := <-done:
			t.Fatalf("Enter の前に終わった (error = %v, stdout = %q)", err, out.String())
		case <-time.After(500 * time.Millisecond):
		}
		if got := readLines(t, filepath.Join(rex.dir, "calls.log")); len(got) != 0 {
			t.Fatalf("Enter の前に rex を呼んだ: %q", got)
		}
		answerPrompt(t, cmd, stdin, interrupt)
	}
	// stdin は終わるまで開けておく。先に閉じると EOF で read が抜け、Ctrl-C で抜けたのか分からない。
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("終わらない (stdout = %q)", out.String())
	}
	if cerr := stdin.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
		t.Fatalf("stdin を閉じられない: %v", cerr)
	}
	wantMsg := "exit " + strconv.Itoa(exit) + " で終わった"
	if (exit != 0) != strings.Contains(out.String(), wantMsg) {
		t.Errorf("stdout = %q, %q を含むのは異常終了のときだけ", out.String(), wantMsg)
	}
	var exitErr *exec.ExitError
	switch {
	case exit == 0 && err != nil:
		t.Fatalf("包んだ argv の実行 error = %v, want nil", err)
	case exit != 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != exit):
		t.Fatalf("包んだ argv の実行 error = %v, want exit %d", err, exit)
	}
	return out.String(), err
}

// TestExecClient_NewSession_ClosesOnExit は rex が起こす argv を実際に走らせ、session (REX_SESSION) に残る block が
// 自分 (REX_BLOCK) だけのときだけ session を閉じ、exit code をそのまま返すことを見る。0 以外で終わったときは終わり方を
// 出して Enter を待ち、押されるまで失敗の画面を残す。後から開いた pane があれば巻き込まないよう閉じない (#391)。
// どの場合も、コマンドを起こす前に agents-daemon で session の印を書く (後から pane が空になった session を
// daemon が閉じる。#557)。包みの中は rex の CLI を叩く。
func TestExecClient_NewSession_ClosesOnExit(t *testing.T) {
	const (
		own   = `{"block_id":"block:own","window_id":"window:1"}`
		other = `{"block_id":"block:other","window_id":"window:2"}`
	)
	blockLs := []string{"--autostart=false", "block", "ls", "-s", "session:spawned", "--json"}
	kill := []string{"--autostart=false", "session", "kill", "session:spawned"}
	tests := []struct {
		name      string
		exit      int
		blocks    string // block ls の応答。空なら fixture を置かない (rex が失敗する)
		interrupt bool   // 異常終了の待ちを Enter ではなく SIGINT (Ctrl-C) で抜ける
		wantAfter [][]string
	}{
		{name: "異常終了の待ちは Ctrl-C でも session を閉じる", exit: 3, blocks: `{"blocks":[` + own + `]}`, interrupt: true, wantAfter: [][]string{blockLs, kill}},
		{name: "正常終了で自分の block だけなら session を閉じる", exit: 0, blocks: `{"blocks":[` + own + `]}`, wantAfter: [][]string{blockLs, kill}},
		{name: "ほかの pane が残っていれば閉じない", exit: 0, blocks: `{"blocks":[` + own + `,` + other + `]}`, wantAfter: [][]string{blockLs}},
		{name: "残る 1 つが自分でなければ閉じない", exit: 0, blocks: `{"blocks":[` + other + `]}`, wantAfter: [][]string{blockLs}},
		{name: "block ls が失敗したら閉じない", exit: 0, wantAfter: [][]string{blockLs}},
		{name: "異常終了は Enter の後に session を閉じる", exit: 3, blocks: `{"blocks":[` + own + `]}`, wantAfter: [][]string{blockLs, kill}},
		{name: "異常終了でもほかの pane が残っていれば閉じない", exit: 3, blocks: `{"blocks":[` + own + `,` + other + `]}`, wantAfter: [][]string{blockLs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := spawnAPI(t)
			rex := newFakeRex(t)
			if tt.blocks != "" {
				rex.put(t, "block-ls-session:spawned.json", tt.blocks)
			}
			useFakeSelf(t, rex)
			inner := []string{"/bin/sh", "-c", "exit " + strconv.Itoa(tt.exit)}
			if _, err := f.client(staticResolver(nil)).NewSession(context.Background(), "x", "/w/x", inner); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			runWrapped(t, spawnedArgv(t, f), []string{"REX_SESSION=session:spawned", "REX_BLOCK=block:own"}, tt.exit, tt.interrupt, rex)

			if got := splitArgs(t, readLines(t, filepath.Join(rex.dir, "calls.log"))); !slices.EqualFunc(got, tt.wantAfter, slices.Equal[[]string]) {
				t.Errorf("calls =\n%q\nwant\n%q", got, tt.wantAfter)
			}
			wantMark := [][]string{{"spawned-session", "mark", "session:spawned"}}
			if got := splitArgs(t, readLines(t, filepath.Join(rex.dir, "self.log"))); !slices.EqualFunc(got, wantMark, slices.Equal[[]string]) {
				t.Errorf("agents-daemon の呼び出し = %q, want %q", got, wantMark)
			}
		})
	}
}

// TestExecClient_NewSession_OwnProcessGroup は、包んだコマンドが sh と別のプロセスグループで動くことを見る。
// sh と同じグループのままだと rex は sh を前景と見て、claude が立てた端末の題を sh のものとして扱い、
// サイドバーに出さない (#570)。
// macOS の /bin/sh (bash 3.2) は端末が無くても set -m でグループを分けるが、Linux の dash は端末が無いと
// job control を切るので、rex が動く darwin だけで見る。
func TestExecClient_NewSession_OwnProcessGroup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("包みが相手にするのは macOS の /bin/sh。dash は端末が無いと set -m を効かせない")
	}
	f := spawnAPI(t)
	rex := newFakeRex(t)
	rex.put(t, "block-ls-session:spawned.json", `{"blocks":[]}`)
	useFakeSelf(t, rex)
	inner := []string{"perl", "-e", `print getpgrp() == $$ ? "own" : "shared"`}
	if _, err := f.client(staticResolver(nil)).NewSession(context.Background(), "x", "/w/x", inner); err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	argv := spawnedArgv(t, f)
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- テストが組んだ argv
	cmd.Env = append(os.Environ(), "REX_SESSION=session:spawned", "REX_BLOCK=block:own")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("包んだ argv の実行 error = %v", err)
	}
	if got := string(out); got != "own" {
		t.Errorf("包んだコマンドのプロセスグループ = %q, want %q (sh と別のグループ)", got, "own")
	}
}

// answerPrompt は異常終了の後の「Enter で閉じる」の待ちに、Enter か Ctrl-C (SIGINT) で答える。
func answerPrompt(t *testing.T, cmd *exec.Cmd, stdin io.Writer, interrupt bool) {
	t.Helper()
	if interrupt {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatalf("SIGINT を送れない: %v", err)
		}
		return
	}
	if _, err := io.WriteString(stdin, "\n"); err != nil {
		t.Fatalf("Enter を送れない: %v", err)
	}
}

func TestExecClient_NewSession_Failures(t *testing.T) {
	tests := []struct {
		name    string
		reply   string // session.create の応答。空なら失敗を返す
		wantErr string
	}{
		{name: "rex が失敗したら message を添える", wantErr: "too many sessions"},
		{name: "window が無ければ pane が分からないので error", reply: `{"session_id":"session:x","initial_windows":[]}`, wantErr: "block_ids"},
		{name: "block_ids が空なら pane が分からないので error", reply: `{"session_id":"session:x","initial_windows":[{"window_id":"window:x","block_ids":[],"revision":1}]}`, wantErr: "block_ids"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			if tt.reply == "" {
				f.Protocol().Fail("session.create", &controltest.Error{Code: "conflict", Message: "too many sessions", Status: 409})
			} else {
				f.Protocol().Reply("session.create", json.RawMessage(tt.reply))
			}
			rex := newFakeRex(t)
			useFakeSelf(t, rex)
			_, err := f.client(staticResolver(nil)).NewSession(context.Background(), "x", "/w/x", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewSession() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// windowNewJSON は session.new_window の応答。形は 2026-11-17 の実測。
const (
	windowBlock   = "block:01a1e34c-5b6c-7101-a782-d4602aeca5d0"
	windowID      = "window:01a1e34c-5b7b-7a1d-9408-649609748f1e"
	windowNewJSON = `{"window_id":"` + windowID + `","block_ids":["` + windowBlock + `"],"revision":2}`
	sharedSession = "session:01a1e34c-4967-743d-a75c-0b5c50b43026"
)

// windowAPI は足し先の session があり、session.new_window が windowNewJSON を返す fake の API を返す。
func windowAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.AddUnlisted(sharedSession, [2]string{windowBlock, wire.ShellFlavor})
	f.Protocol().Reply("session.new_window", json.RawMessage(windowNewJSON))
	return f
}

// newWindowRequest は session.new_window の payload。
type newWindowRequest struct {
	SessionID   string  `json:"session_id"`
	WindowLabel *string `json:"window_label"`
	Focus       *bool   `json:"focus"`
	Layout      struct {
		Block blockOptions `json:"block"`
	} `json:"layout"`
}

// TestExecClient_NewWindow は既存の rex session にタブ (window) を足してコマンドを起こす配線を見る。足し先は
// target.resolve で ID に引き (label でも名指せる)、利用者が見ている window を動かさないよう focus を false にする。
// argv は wrap のタブの包みの後にそのまま渡し、作った block は作った client の持ち物にしない。
func TestExecClient_NewWindow(t *testing.T) {
	f := windowAPI(t)
	f.AddLabel(sharedSession, "work")
	c := f.client(staticResolver(nil))

	argv := []string{"claude", "--session-id", sidDone, "--", "パーキングロットのメモ「x」 a b"}
	got, err := c.NewWindow(context.Background(), "work", "claude-config", "/w/claude-config", argv)
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	// 足し先は label で指しても、返す session は resolve した ID (畳むときに label の rename で外れない)。
	if want := (multiplexer.Placement{SessionID: sharedSession, WindowID: windowID, PaneID: windowBlock}); got != want {
		t.Errorf("NewWindow() = %+v, want %+v", got, want)
	}
	f.assertCalls(t,
		"target.resolve work",
		"session.new_window "+sharedSession,
		"session.attach "+sharedSession,
		"session.disown_block "+sharedSession,
		"session.detach "+sharedSession,
	)
	var req newWindowRequest
	f.payloadOf(t, "session.new_window", &req)
	if !labelIs(req.WindowLabel, "claude-config") || req.Focus == nil || *req.Focus {
		t.Errorf("window_label = %v, focus = %v", req.WindowLabel, req.Focus)
	}
	block := req.Layout.Block
	if block.Flavor != wire.ShellFlavor || block.Options.CWD != "/w/claude-config" || !slices.Equal(block.Options.Command, wrap.TabCommand(argv)) {
		t.Errorf("block = %+v", block)
	}
	if block.Options.Shell != nil || block.Options.Login != nil || block.Options.Exit != nil {
		t.Errorf("shell / login / exit を既定から変えた: %+v", block.Options)
	}
	var resolve struct {
		Kinds []string `json:"kinds"`
	}
	f.payloadOf(t, "target.resolve", &resolve)
	if !slices.Equal(resolve.Kinds, []string{"session"}) {
		t.Errorf("target.resolve の kinds = %v", resolve.Kinds)
	}
}

// 足し先の label は実機と同じく大小文字を区別せずに引く。同じ label の session が 2 つあれば、どちらに足すかを
// 決めずに error にし、閉じた session (not found) とは分ける (呼び手が「無い」と誤読しない)。
func TestExecClient_NewWindow_LabelMatching(t *testing.T) {
	t.Run("大小文字を区別しない", func(t *testing.T) {
		f := windowAPI(t)
		f.AddLabel(sharedSession, "work")
		if _, err := f.client(staticResolver(nil)).NewWindow(context.Background(), "Work", "x", "/w/x", []string{"claude"}); err != nil {
			t.Fatalf("NewWindow() error = %v", err)
		}
	})
	t.Run("2 つに当たれば ambiguous", func(t *testing.T) {
		f := windowAPI(t)
		f.AddLabel(sharedSession, "work")
		f.AddUnlisted(sessionB)
		f.AddLabel(sessionB, "WORK")
		_, err := f.client(staticResolver(nil)).NewWindow(context.Background(), "work", "x", "/w/x", []string{"claude"})
		if err == nil || !strings.Contains(err.Error(), "ambiguous") || isNotFound(err) {
			t.Errorf("NewWindow() error = %v, want not found でない ambiguous の error", err)
		}
		if calls := f.Calls(); slices.Contains(calls, "session.new_window "+sharedSession) {
			t.Errorf("ambiguous なのに window を足した: %v", calls)
		}
	})
}

// label は window の名前で、先頭の `-` は NewSession と同じく外す。外して空なら label を付けない。
func TestExecClient_NewWindow_DashLabel(t *testing.T) {
	for _, tt := range []struct{ label, want string }{{"-x", "x"}, {"--", ""}} {
		t.Run(tt.label, func(t *testing.T) {
			f := windowAPI(t)
			if _, err := f.client(staticResolver(nil)).NewWindow(context.Background(), sharedSession, tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			var req newWindowRequest
			f.payloadOf(t, "session.new_window", &req)
			if !labelIs(req.WindowLabel, tt.want) {
				t.Errorf("window_label = %v, want %q", req.WindowLabel, tt.want)
			}
		})
	}
}

// TestExecClient_NewWindow_ExitKeepsSession は rex が起こす argv を実際に走らせ、タブの包みが共有の session に
// 触れないことを見る。印を書かず、session も閉じない (rex を呼ばない)。0 以外で終わったときは Enter か Ctrl-C まで
// 失敗の画面を残し、exit code をそのまま返す。起きたコマンドには共有の session にいることを環境変数で渡す。
func TestExecClient_NewWindow_ExitKeepsSession(t *testing.T) {
	const sharedEnv = "AGENTS_DAEMON_SHARED_REX_SESSION" // hooks/shared-rex-session.sh が読む
	tests := []struct {
		name      string
		exit      int
		interrupt bool
	}{
		{name: "正常終了", exit: 0},
		{name: "異常終了は Enter まで残す", exit: 3},
		{name: "異常終了の待ちは Ctrl-C でも抜ける", exit: 3, interrupt: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := windowAPI(t)
			rex := newFakeRex(t)
			useFakeSelf(t, rex)
			envOut := filepath.Join(rex.dir, "env.txt")
			inner := []string{"/bin/sh", "-c", `printf %s "$` + sharedEnv + `" > "$0"; exit ` + strconv.Itoa(tt.exit), envOut}
			if _, err := f.client(staticResolver(nil)).NewWindow(context.Background(), sharedSession, "x", "/w/x", inner); err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			var req newWindowRequest
			f.payloadOf(t, "session.new_window", &req)
			runWrapped(t, req.Layout.Block.Options.Command, []string{"REX_SESSION=" + sharedSession, "REX_BLOCK=block:own"}, tt.exit, tt.interrupt, rex)

			if got := readLines(t, filepath.Join(rex.dir, "calls.log")); len(got) != 0 {
				t.Errorf("包みが rex を呼んだ (共有の session に触れない): %q", got)
			}
			if _, err := os.Stat(filepath.Join(rex.dir, "self.log")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("包みが agents-daemon を呼んだ (共有の session に印を書かない): stat error = %v", err)
			}
			b, err := os.ReadFile(envOut) // #nosec G304 -- テストの一時ファイル
			if err != nil {
				t.Fatalf("起きたコマンドの環境を読めない: %v", err)
			}
			if string(b) != "1" {
				t.Errorf("起きたコマンドの %s = %q, want %q", sharedEnv, b, "1")
			}
		})
	}
}

func TestExecClient_NewWindow_Failures(t *testing.T) {
	tests := []struct {
		name      string
		session   string
		setup     func(f *fakeAPI)
		wantErr   string
		wantCalls bool
	}{
		{name: "足し先が空なら rex を呼ばずに error", session: "", setup: func(*fakeAPI) {}, wantErr: "足し先"},
		{name: "足し先の session が無ければ error", session: "gone", setup: func(*fakeAPI) {}, wantErr: "not found", wantCalls: true},
		{name: "rex が失敗したら message を添える", session: sharedSession, setup: func(f *fakeAPI) {
			f.AddUnlisted(sharedSession)
			f.Protocol().Fail("session.new_window", &controltest.Error{Code: "conflict", Message: "too many blocks", Status: 409})
		}, wantErr: "too many blocks", wantCalls: true},
		{name: "block_ids が空なら pane が分からないので error", session: sharedSession, setup: func(f *fakeAPI) {
			f.AddUnlisted(sharedSession)
			f.Protocol().Reply("session.new_window", json.RawMessage(`{"window_id":"window:x","block_ids":[],"revision":1}`))
		}, wantErr: "block_ids", wantCalls: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			tt.setup(f)
			_, err := f.client(staticResolver(nil)).NewWindow(context.Background(), tt.session, "x", "/w/x", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewWindow() error = %v, want containing %q", err, tt.wantErr)
			}
			if called := len(f.Calls()) > 0; called != tt.wantCalls {
				t.Errorf("rex を呼んだ = %v, want %v", called, tt.wantCalls)
			}
		})
	}
}

// SplitPane は anchor の block の下に block を作り、shell を挟まず argv を直に起こす (simulate の使い捨て pane)。
// CLI の split --split=below --ratio 40 --focus=false --keep-open --shell=none と同じ形にする:
// ratio は上 (anchor) の取り分で 0.6、login を切り (切らないと /usr/bin/login を挟んで exit code が 0 に化ける)、
// 終わっても block を残す。作った block は作った client の持ち物にしない。
func TestExecClient_SplitPane(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	f.Protocol().Reply("session.new_split", json.RawMessage(`{"anchor_id":"`+blockClaudeWorking+`","block_ids":["block:01a1-new"],"split_ids":["split:01a1"],"revision":4}`))
	argv := []string{"/bin/sh", "/tmp/sim/simulate.sh", "/tmp/sim/screen.txt"}

	got, err := f.client(staticResolver(nil)).SplitPane(context.Background(), blockClaudeWorking, "agents-daemon-simulate", argv)
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	if got != "block:01a1-new" {
		t.Errorf("SplitPane() = %q, want block:01a1-new", got)
	}
	f.assertCalls(t,
		"target.resolve "+blockClaudeWorking,
		"session.new_split "+sessionA,
		"session.attach "+sessionA,
		"session.disown_block "+sessionA,
		"session.detach "+sessionA,
	)
	var req struct {
		AnchorID  string  `json:"anchor_id"`
		Direction string  `json:"direction"`
		Side      string  `json:"side"`
		Ratio     float64 `json:"ratio"`
		Focus     *bool   `json:"focus"`
		Layout    struct {
			Block blockOptions `json:"block"`
		} `json:"layout"`
	}
	f.payloadOf(t, "session.new_split", &req)
	if req.AnchorID != blockClaudeWorking || req.Direction != "vertical" || req.Side != "after" || req.Ratio != 0.6 || req.Focus == nil || *req.Focus {
		t.Errorf("new_split = %+v", req)
	}
	o := req.Layout.Block.Options
	if req.Layout.Block.Label != "agents-daemon-simulate" || !slices.Equal(o.Command, argv) ||
		o.Shell == nil || *o.Shell != "none" || o.Login == nil || *o.Login || o.Exit == nil || o.Exit.OnCompletion {
		t.Errorf("block = %+v", req.Layout.Block)
	}
}

// ClosePane は block を block.close で閉じる。無い block は not found の error になる。
func TestExecClient_ClosePane(t *testing.T) {
	f := newFakeAPI(t)
	f.AddSession(sessionA, [2]string{blockClaudeWorking, wire.ShellFlavor})
	c := f.client(staticResolver(nil))
	if err := c.ClosePane(context.Background(), blockClaudeWorking); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
	f.assertCalls(t, "target.resolve "+blockClaudeWorking, "block.close "+blockClaudeWorking)
	if err := c.ClosePane(context.Background(), blockClosed); !isNotFound(err) {
		t.Errorf("無い block の ClosePane() error = %v, want not found", err)
	}
}
