package rexcli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sessionNewJSON は `session new --json` の応答。形は 2026-11-07 の実測。
const sessionNewJSON = `{"session_id":"session:01a1b37a-0560-743d-adfd-959e5e1c53a9","initial_windows":[{"window_id":"window:01a1b37a-056e-70fa-90fd-15d50af005a9","block_ids":["block:01a1b37a-0560-74ce-af88-d37d23de33b9"],"revision":1}]}`

// wrapPrefix は NewSession が argv の前に置く、終了を見張る sh の語。
func wrapPrefix(f *fakeRex) string {
	return "/bin/sh -c " + shellQuote(closeOnExitScript) + " agents-daemon-spawn " + shellQuote(f.bin)
}

// TestExecClient_NewSession は新しい rex session でコマンドを起こす配線を見る。起こす側の session を指さず (-s を付けない)、
// argv は sh の包みの後にそのまま渡す (空白を含む引数も 1 つのまま。改行を含む引数が 1 つのまま
// 子へ届くことは fake の calls.log が行で割れるのでここでは見ず、実機で確かめた)。失敗の画面は包みが Enter まで残すので、
// --keep-open は付けない (付けると終わった block が残り、session に空の pane が溜まる)。
func TestExecClient_NewSession(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "session-new.json", sessionNewJSON)
	c := f.client(staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	got, err := c.NewSession(context.Background(), "claude-config", "/w/claude-config", []string{"claude", "--session-id", sidDone, "--", prompt})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if want := "block:01a1b37a-0560-74ce-af88-d37d23de33b9"; got != want {
		t.Errorf("NewSession() = %q, want %q", got, want)
	}
	f.assertCalls(t,
		"--autostart=false session new claude-config --cwd /w/claude-config --window claude-config --json -- "+wrapPrefix(f)+" claude --session-id "+sidDone+" -- "+shellQuote(prompt),
	)
}

// rex は `-` で始まる LABEL を flag と読んで拒むので (実測)、label の先頭の `-` を外す。外して空なら label を付けない。
func TestExecClient_NewSession_DashLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{label: "-x", want: "--autostart=false session new x --cwd /w/-x --window x --json -- "},
		{label: "--", want: "--autostart=false session new --cwd /w/-x --json -- "},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			f := newFakeRex(t)
			f.put(t, "session-new.json", sessionNewJSON)
			c := f.client(staticResolver(nil))
			if _, err := c.NewSession(context.Background(), tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			f.assertCalls(t, tt.want+wrapPrefix(f)+" claude")
		})
	}
}

// TestExecClient_NewSession_ClosesOnExit は rex が起こす argv を実際に走らせ、session (REX_SESSION) に残る block が
// 自分 (REX_BLOCK) だけのときだけ session を閉じ、exit code をそのまま返すことを見る。0 以外で終わったときは終わり方を
// 出して Enter を待ち、押されるまで失敗の画面を残す。後から開いた pane があれば巻き込まないよう閉じない (#391)。
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
			f := newFakeRex(t)
			f.put(t, "session-new.json", sessionNewJSON)
			if tt.blocks != "" {
				f.put(t, "block-ls-session:spawned.json", tt.blocks)
			}
			c := f.client(staticResolver(nil))
			inner := []string{"/bin/sh", "-c", "exit " + strconv.Itoa(tt.exit)}
			if _, err := c.NewSession(context.Background(), "x", "/w/x", inner); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			calls := splitArgs(t, readLines(t, filepath.Join(f.dir, "calls.log")))
			if len(calls) != 1 {
				t.Fatalf("calls = %q, want 1 call", calls)
			}
			sep := slices.Index(calls[0], "--")
			if sep < 0 {
				t.Fatalf("session new の引数に -- が無い: %q", calls[0])
			}
			argv := calls[0][sep+1:]

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
				if got := readLines(t, filepath.Join(f.dir, "calls.log")); len(got) != 1 {
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

			want := append([][]string{calls[0]}, tt.wantAfter...)
			if got := splitArgs(t, readLines(t, filepath.Join(f.dir, "calls.log"))); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
				t.Errorf("calls =\n%q\nwant\n%q", got, want)
			}
		})
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
		fixture map[string]string
		wantErr string
	}{
		{name: "rex が失敗したら stderr を添える", fixture: map[string]string{"session-new.json": sessionNewJSON, "fail-session-new.json": ""}, wantErr: "server error"},
		{name: "window が無ければ pane が分からないので error", fixture: map[string]string{"session-new.json": `{"session_id":"session:x","initial_windows":[]}`}, wantErr: "block_ids"},
		{name: "block_ids が空なら pane が分からないので error", fixture: map[string]string{"session-new.json": `{"session_id":"session:x","initial_windows":[{"window_id":"window:x","block_ids":[],"revision":1}]}`}, wantErr: "block_ids"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			for name, content := range tt.fixture {
				f.put(t, name, content)
			}
			c := f.client(staticResolver(nil))
			_, err := c.NewSession(context.Background(), "x", "/w/x", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewSession() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
