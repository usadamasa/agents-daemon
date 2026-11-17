package rexcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control/controltest"
)

// windowNewJSON は session.new_window の応答。形は 2026-11-17 の実測。
const windowNewJSON = `{"window_id":"window:01a1e34c-5b7b-7a1d-9408-649609748f1e","block_ids":["block:01a1e34c-5b6c-7101-a782-d4602aeca5d0"],"revision":2}`

const sharedSession = "session:01a1e34c-4967-743d-a75c-0b5c50b43026"

// windowAPI は足し先の session があり、session.new_window が windowNewJSON を返す fake の API を返す。
func windowAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.addUnlisted(sharedSession, [2]string{"block:01a1e34c-5b6c-7101-a782-d4602aeca5d0", blockstate.ShellFlavor})
	f.srv.Reply("session.new_window", json.RawMessage(windowNewJSON))
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
// argv は sh の包みの後にそのまま渡し、作った block は CLI と同じく作った client の持ち物にしない。
func TestExecClient_NewWindow(t *testing.T) {
	f := windowAPI(t)
	c := f.client(staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	argv := []string{"claude", "--session-id", sidDone, "--", prompt}
	got, err := c.NewWindow(context.Background(), sharedSession, "claude-config", "/w/claude-config", argv)
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if want := "block:01a1e34c-5b6c-7101-a782-d4602aeca5d0"; got != want {
		t.Errorf("NewWindow() = %q, want %q", got, want)
	}
	f.assertCalls(t,
		"target.resolve "+sharedSession,
		"session.new_window "+sharedSession,
		"session.attach "+sharedSession,
		"session.disown_block "+sharedSession,
	)
	var req newWindowRequest
	f.payloadOf(t, "session.new_window", &req)
	wantCommand := append([]string{"/bin/sh", "-c", closeOnExitTabScript, "agents-daemon-spawn"}, argv...)
	if req.WindowLabel == nil || *req.WindowLabel != "claude-config" || req.Focus == nil || *req.Focus {
		t.Errorf("window_label = %v, focus = %v", req.WindowLabel, req.Focus)
	}
	block := req.Layout.Block
	if block.Flavor != blockstate.ShellFlavor || block.Options.CWD != "/w/claude-config" || !slices.Equal(block.Options.Command, wantCommand) {
		t.Errorf("block = %+v\nwant command %q", block, wantCommand)
	}
	if block.Options.Shell != nil || block.Options.Login != nil || block.Options.Exit != nil {
		t.Errorf("shell / login / exit を既定から変えた: %+v", block.Options)
	}
}

// 足し先は label でも名指せる (CLI の window new -s と同じ)。session.new_window には引いた ID を渡す。
func TestExecClient_NewWindow_SessionLabel(t *testing.T) {
	f := windowAPI(t)
	f.addLabel(sharedSession, "work")
	if _, err := f.client(staticResolver(nil)).NewWindow(context.Background(), "work", "x", "/w/x", []string{"claude"}); err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	var req newWindowRequest
	f.payloadOf(t, "session.new_window", &req)
	if req.SessionID != sharedSession {
		t.Errorf("session.new_window の session_id = %q, want %q", req.SessionID, sharedSession)
	}
	var resolve struct {
		Kinds   []string `json:"kinds"`
		Queries []string `json:"queries"`
	}
	f.payloadOf(t, "target.resolve", &resolve)
	if !slices.Equal(resolve.Kinds, []string{"session"}) || !slices.Equal(resolve.Queries, []string{"work"}) {
		t.Errorf("target.resolve = %+v", resolve)
	}
}

// label は window の名前で、先頭の `-` は NewSession と同じく外す。外して空なら label を付けない。
func TestExecClient_NewWindow_DashLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{label: "-x", want: "x"},
		{label: "--", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			f := windowAPI(t)
			if _, err := f.client(staticResolver(nil)).NewWindow(context.Background(), sharedSession, tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			var req newWindowRequest
			f.payloadOf(t, "session.new_window", &req)
			got := ""
			if req.WindowLabel != nil {
				got = *req.WindowLabel
			}
			if got != tt.want || (tt.want == "" && req.WindowLabel != nil) {
				t.Errorf("window_label = %v, want %q", req.WindowLabel, tt.want)
			}
		})
	}
}

// TestExecClient_NewWindow_ExitKeepsSession は rex が起こす argv を実際に走らせ、タブの包みが共有の session に
// 触れないことを見る。印を書かず、session も閉じない (rex を呼ばない)。claude が終われば pane が閉じ、最後の pane を
// 失った window は rex が閉じる (実測) ので、ほかのタブは残る。0 以外で終わったときは Enter か Ctrl-C まで失敗の画面を
// 残し、exit code をそのまま返す。起きたコマンドには共有の session にいることを環境変数で渡す。
func TestExecClient_NewWindow_ExitKeepsSession(t *testing.T) {
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
			c := f.clientWith(rex, staticResolver(nil))
			envOut := filepath.Join(rex.dir, "env.txt")
			inner := []string{"/bin/sh", "-c", `printf %s "$` + SharedSessionEnv + `" > "$0"; exit ` + strconv.Itoa(tt.exit), envOut}
			if _, err := c.NewWindow(context.Background(), sharedSession, "x", "/w/x", inner); err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			var req newWindowRequest
			f.payloadOf(t, "session.new_window", &req)
			argv := req.Layout.Block.Options.Command

			cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- テストが組んだ argv
			cmd.Env = append(os.Environ(), "REX_SESSION="+sharedSession, "REX_BLOCK=block:own")
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
				select {
				case err := <-done:
					t.Fatalf("Enter の前に終わった (error = %v, stdout = %q)", err, stdout.String())
				case <-time.After(500 * time.Millisecond):
				}
				answerPrompt(t, cmd, stdin, tt.interrupt)
			}
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
				t.Errorf("起きたコマンドの %s = %q, want %q", SharedSessionEnv, b, "1")
			}
		})
	}
}

// peer-sessions skill はタブで立てる手順にも closeOnExitTabScript と同じ本文を single quote で囲んで載せる。
func TestCloseOnExitTabScript_MatchesPeerSessionsSkill(t *testing.T) {
	if strings.Contains(closeOnExitTabScript, "'") {
		t.Fatalf("closeOnExitTabScript に single quote がある (skill が single quote で囲めない): %s", closeOnExitTabScript)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "skills", "peer-sessions", "references", "rex.md"))
	if err != nil {
		t.Fatalf("skill の読み込みに失敗: %v", err)
	}
	if !strings.Contains(string(b), "/bin/sh -c '"+closeOnExitTabScript+"'") {
		t.Errorf("peer-sessions skill の references/rex.md に closeOnExitTabScript と同じ本文が無い:\n%s", closeOnExitTabScript)
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
			f.addUnlisted(sharedSession)
			f.srv.Fail("session.new_window", &controltest.Error{Code: "conflict", Message: "too many blocks", Status: 409})
		}, wantErr: "too many blocks", wantCalls: true},
		{name: "block_ids が空なら pane が分からないので error", session: sharedSession, setup: func(f *fakeAPI) {
			f.addUnlisted(sharedSession)
			f.srv.Reply("session.new_window", json.RawMessage(`{"window_id":"window:x","block_ids":[],"revision":1}`))
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
			if called := len(f.calls()) > 0; called != tt.wantCalls {
				t.Errorf("rex を呼んだ = %v, want %v", called, tt.wantCalls)
			}
		})
	}
}
