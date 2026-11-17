package rexcli

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

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control/controltest"
)

// spawned の session と block は session.create の応答。形は 2026-11-17 の実測。
const (
	spawnedSession = "session:01a1b37a-0560-743d-adfd-959e5e1c53a9"
	spawnedBlock   = "block:01a1b37a-0560-74ce-af88-d37d23de33b9"
	sessionNewJSON = `{"session_id":"` + spawnedSession + `","initial_windows":[{"window_id":"window:01a1b37a-056e-70fa-90fd-15d50af005a9","block_ids":["` + spawnedBlock + `"],"revision":1}]}`
)

// useFakeSelf は包みが印を書くのに呼ぶ agents-daemon を、引数を self.log へ 1 行ずつ書くだけの script に差し替える。
func useFakeSelf(t *testing.T, f *fakeRex) string {
	t.Helper()
	path := filepath.Join(f.dir, "agents-daemon")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s ' \"$a\"; done >> " + shellQuote(filepath.Join(f.dir, "self.log")) +
		"\necho >> " + shellQuote(filepath.Join(f.dir, "self.log")) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { // #nosec G306 -- テストの実行ファイル
		t.Fatal(err)
	}
	saved := selfBinary
	selfBinary = func() (string, error) { return path, nil }
	t.Cleanup(func() { selfBinary = saved })
	return path
}

// spawnAPI は session.create が spawned の session を返し、その session に attach と disown が通る fake の API を返す。
func spawnAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.srv.Reply("session.create", json.RawMessage(sessionNewJSON))
	f.addUnlisted(spawnedSession, [2]string{spawnedBlock, blockstate.ShellFlavor})
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

// TestExecClient_NewSession は新しい rex session でコマンドを起こす配線を見る。argv は sh の包みの後にそのまま command へ
// 渡す (空白や `$` を含む引数も 1 つのまま。実機で確かめた)。CLI の session new と同じく shell と login は既定のまま
// (login shell の環境で起こす) にし、失敗の画面は包みが Enter まで残すので exit も既定のままにする。
// 作った block は CLI と同じく、作った client の持ち物にしない (session.attach の後に disown_block で手放す)。
func TestExecClient_NewSession(t *testing.T) {
	f := spawnAPI(t)
	rex := newFakeRex(t)
	self := useFakeSelf(t, rex)
	c := f.clientWith(rex, staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	argv := []string{"claude", "--session-id", sidDone, "--", prompt}
	got, err := c.NewSession(context.Background(), "example-repo", "/w/example-repo", argv)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got != spawnedBlock {
		t.Errorf("NewSession() = %q, want %q", got, spawnedBlock)
	}
	f.assertCalls(t, "session.create", "session.attach "+spawnedSession, "session.disown_block "+spawnedSession)

	var req createRequest
	f.payloadOf(t, "session.create", &req)
	if req.Label == nil || *req.Label != "example-repo" || len(req.InitialWindows) != 1 ||
		req.InitialWindows[0].WindowLabel == nil || *req.InitialWindows[0].WindowLabel != "example-repo" {
		t.Errorf("session.create の label と window_label = %+v", req)
	}
	block := req.InitialWindows[0].Layout.Block
	wantCommand := append([]string{"/bin/sh", "-c", closeOnExitScript, "agents-daemon-spawn", rex.bin, self}, argv...)
	if block.Flavor != blockstate.ShellFlavor || block.Options.CWD != "/w/example-repo" || !slices.Equal(block.Options.Command, wantCommand) {
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
			if _, err := f.clientWith(rex, staticResolver(nil)).NewSession(context.Background(), tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			var req createRequest
			f.payloadOf(t, "session.create", &req)
			gotLabel, gotWindow := "", ""
			if req.Label != nil {
				gotLabel = *req.Label
			}
			if req.InitialWindows[0].WindowLabel != nil {
				gotWindow = *req.InitialWindows[0].WindowLabel
			}
			if gotLabel != tt.want || gotWindow != tt.want || (tt.want == "" && (req.Label != nil || req.InitialWindows[0].WindowLabel != nil)) {
				t.Errorf("label = %v, window_label = %v, want %q", req.Label, req.InitialWindows[0].WindowLabel, tt.want)
			}
		})
	}
}

// spawnedArgv は session.create に渡した command を返す。rex が block の中で起こす argv そのもの。
func spawnedArgv(t *testing.T, f *fakeAPI) []string {
	t.Helper()
	var req createRequest
	f.payloadOf(t, "session.create", &req)
	return req.InitialWindows[0].Layout.Block.Options.Command
}

// TestExecClient_NewSession_ClosesOnExit は rex が起こす argv を実際に走らせ、session (REX_SESSION) に残る block が
// 自分 (REX_BLOCK) だけのときだけ session を閉じ、exit code をそのまま返すことを見る。0 以外で終わったときは終わり方を
// 出して Enter を待ち、押されるまで失敗の画面を残す。後から開いた pane があれば巻き込まないよう閉じない (#391)。
// どの場合も、コマンドを起こす前に agents-daemon で session の印を書く (後から pane が空になった session を
// daemon が閉じる。#557)。包みの中は rex の CLI を叩く (この包みは control API へ移していない)。
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
			c := f.clientWith(rex, staticResolver(nil))
			inner := []string{"/bin/sh", "-c", "exit " + strconv.Itoa(tt.exit)}
			if _, err := c.NewSession(context.Background(), "x", "/w/x", inner); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			argv := spawnedArgv(t, f)

			cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- テストが組んだ argv
			cmd.Env = append(os.Environ(), "REX_SESSION=session:spawned", "REX_BLOCK=block:own")
			var stdout strings.Builder
			cmd.Stdout = &stdout
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("StdinPipe() error = %v", err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("包んだ argv の起動 error = %v", err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			if tt.exit != 0 {
				// Enter を押すまでは終わらず、session も閉じない。
				select {
				case err := <-done:
					t.Fatalf("Enter の前に終わった (error = %v, stdout = %q)", err, stdout.String())
				case <-time.After(500 * time.Millisecond):
				}
				if got := readLines(t, filepath.Join(rex.dir, "calls.log")); len(got) != 0 {
					t.Fatalf("Enter の前に rex を呼んだ: %q", got)
				}
				answerPrompt(t, cmd, stdin, tt.interrupt)
			}
			// stdin は終わるまで開けておく。先に閉じると EOF で read が抜け、Ctrl-C で抜けたのか分からない。
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Fatalf("終わらない (stdout = %q)", stdout.String())
			}
			if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Fatalf("stdin を閉じられない: %v", err)
			}
			wantMsg := "exit " + strconv.Itoa(tt.exit) + " で終わった"
			if got := stdout.String(); (tt.exit != 0) != strings.Contains(got, wantMsg) {
				t.Errorf("stdout = %q, %q を含むのは異常終了のときだけ", got, wantMsg)
			}
			var exitErr *exec.ExitError
			switch {
			case tt.exit == 0 && err != nil:
				t.Fatalf("包んだ argv の実行 error = %v, want nil", err)
			case tt.exit != 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != tt.exit):
				t.Fatalf("包んだ argv の実行 error = %v, want exit %d", err, tt.exit)
			}

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
	c := f.clientWith(rex, staticResolver(nil))
	inner := []string{"perl", "-e", `print getpgrp() == $$ ? "own" : "shared"`}
	if _, err := c.NewSession(context.Background(), "x", "/w/x", inner); err != nil {
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

// peer-sessions skill は peer を起こす手順に closeOnExitScript と同じ本文を single quote で囲んで載せる。
// 片方だけ直してずれないよう、skill の本文に今の定数がそのまま入っていることを見る。
func TestCloseOnExitScript_MatchesPeerSessionsSkill(t *testing.T) {
	if strings.Contains(closeOnExitScript, "'") {
		t.Fatalf("closeOnExitScript に single quote がある (skill が single quote で囲めない): %s", closeOnExitScript)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "skills", "peer-sessions", "references", "rex.md"))
	if err != nil {
		t.Fatalf("skill の読み込みに失敗: %v", err)
	}
	if !strings.Contains(string(b), "/bin/sh -c '"+closeOnExitScript+"'") {
		t.Errorf("peer-sessions skill の references/rex.md に closeOnExitScript と同じ本文が無い:\n%s", closeOnExitScript)
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
				f.srv.Fail("session.create", &controltest.Error{Code: "conflict", Message: "too many sessions", Status: 409})
			} else {
				f.srv.Reply("session.create", json.RawMessage(tt.reply))
			}
			rex := newFakeRex(t)
			useFakeSelf(t, rex)
			_, err := f.clientWith(rex, staticResolver(nil)).NewSession(context.Background(), "x", "/w/x", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewSession() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
