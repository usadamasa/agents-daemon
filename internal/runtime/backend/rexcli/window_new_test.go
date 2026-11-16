package rexcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// windowNewJSON は `window new --json` の応答。形は 2026-11-16 の実測。
const windowNewJSON = `{"window_id":"window:01a1e34c-5b7b-7a1d-9408-649609748f1e","block_ids":["block:01a1e34c-5b6c-7101-a782-d4602aeca5d0"],"revision":2}`

const sharedSession = "session:01a1e34c-4967-743d-a75c-0b5c50b43026"

// tabWrapPrefix は NewWindow が argv の前に置く、終了を見張る sh の語。
func tabWrapPrefix() string {
	return "/bin/sh -c " + shellQuote(closeOnExitTabScript) + " agents-daemon-spawn"
}

// TestExecClient_NewWindow は既存の rex session にタブ (window) を足してコマンドを起こす配線を見る。足し先は -s で
// 指し、利用者が見ている window を動かさないよう --focus=false を付ける。argv は sh の包みの後にそのまま渡す。
func TestExecClient_NewWindow(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "window-new.json", windowNewJSON)
	c := f.client(staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	got, err := c.NewWindow(context.Background(), sharedSession, "claude-config", "/w/claude-config", []string{"claude", "--session-id", sidDone, "--", prompt})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if want := "block:01a1e34c-5b6c-7101-a782-d4602aeca5d0"; got != want {
		t.Errorf("NewWindow() = %q, want %q", got, want)
	}
	f.assertCalls(t,
		"--autostart=false window new -s "+sharedSession+" claude-config --cwd /w/claude-config --focus=false --json -- "+tabWrapPrefix()+" claude --session-id "+sidDone+" -- "+shellQuote(prompt),
	)
}

// LABEL は session new と同じく位置引数なので、先頭の `-` を外す。外して空なら label を付けない。
func TestExecClient_NewWindow_DashLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{label: "-x", want: "--autostart=false window new -s " + sharedSession + " x --cwd /w/-x --focus=false --json -- "},
		{label: "--", want: "--autostart=false window new -s " + sharedSession + " --cwd /w/-x --focus=false --json -- "},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			f := newFakeRex(t)
			f.put(t, "window-new.json", windowNewJSON)
			c := f.client(staticResolver(nil))
			if _, err := c.NewWindow(context.Background(), sharedSession, tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			f.assertCalls(t, tt.want+tabWrapPrefix()+" claude")
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
			f := newFakeRex(t)
			f.put(t, "window-new.json", windowNewJSON)
			useFakeSelf(t, f)
			c := f.client(staticResolver(nil))
			envOut := filepath.Join(f.dir, "env.txt")
			inner := []string{"/bin/sh", "-c", `printf %s "$` + SharedSessionEnv + `" > "$0"; exit ` + strconv.Itoa(tt.exit), envOut}
			if _, err := c.NewWindow(context.Background(), sharedSession, "x", "/w/x", inner); err != nil {
				t.Fatalf("NewWindow() error = %v", err)
			}
			calls := splitArgs(t, readLines(t, filepath.Join(f.dir, "calls.log")))
			if len(calls) != 1 {
				t.Fatalf("calls = %q, want 1 call", calls)
			}
			sep := slices.Index(calls[0], "--")
			if sep < 0 {
				t.Fatalf("window new の引数に -- が無い: %q", calls[0])
			}
			argv := calls[0][sep+1:]

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

			if got := splitArgs(t, readLines(t, filepath.Join(f.dir, "calls.log"))); len(got) != 1 {
				t.Errorf("包みが rex を呼んだ (共有の session に触れない): %q", got[1:])
			}
			if _, err := os.Stat(filepath.Join(f.dir, "self.log")); !errors.Is(err, os.ErrNotExist) {
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
		fixture   map[string]string
		wantErr   string
		wantCalls bool
	}{
		{name: "足し先が空なら rex を呼ばずに error", session: "", fixture: map[string]string{"window-new.json": windowNewJSON}, wantErr: "足し先"},
		{name: "rex が失敗したら stderr を添える", session: sharedSession, fixture: map[string]string{"window-new.json": windowNewJSON, "fail-window-new.json": ""}, wantErr: "server error", wantCalls: true},
		{name: "足し先の session が無ければ error", session: sharedSession, wantErr: "not found", wantCalls: true},
		{name: "block_ids が空なら pane が分からないので error", session: sharedSession, fixture: map[string]string{"window-new.json": `{"window_id":"window:x","block_ids":[],"revision":1}`}, wantErr: "block_ids", wantCalls: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			for name, content := range tt.fixture {
				f.put(t, name, content)
			}
			c := f.client(staticResolver(nil))
			_, err := c.NewWindow(context.Background(), tt.session, "x", "/w/x", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewWindow() error = %v, want containing %q", err, tt.wantErr)
			}
			_, statErr := os.Stat(filepath.Join(f.dir, "calls.log"))
			if called := statErr == nil; called != tt.wantCalls {
				t.Errorf("rex を呼んだ = %v, want %v", called, tt.wantCalls)
			}
		})
	}
}
