package rexcli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
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

	sidWorking = "0d5e717f-82c5-4e71-ac74-799f7197e0de"
	sidDone    = "6b1f3c2a-1111-4222-8333-944455556666"
)

// lsJSON は `ls --json` の応答。形は 2026-10-10 の実測。
func lsJSON(sessions ...string) string {
	parts := make([]string, 0, len(sessions))
	for _, s := range sessions {
		parts = append(parts, `{"session_id":"`+s+`","label":"shy-alcove"}`)
	}
	return `{"sessions":[` + strings.Join(parts, ",") + `]}`
}

// blockLsJSON は `block ls --json` の応答。形は 2026-10-10 の実測。
func blockLsJSON(blocks ...[2]string) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, `{"block_id":"`+b[0]+`","label":"shell","creator_name":"com.superlogical.terminal","flavor":"`+b[1]+`","host_id":"dfd31eaf-0189-45ad-9a36-10c09f3cb111","placement":"placed","window_id":"window:01a1250f-c11a-7a87-b4b5-eca09f61b728"}`)
	}
	return `{"blocks":[` + strings.Join(parts, ",") + `]}`
}

// fakeRex は fixture のディレクトリと、それを読む fake の rex。
type fakeRex struct {
	dir string
	bin string
}

// newFakeRex は引数で fixture を選んで返す fake の rex を書く。fixture が無ければ実機と同じく
// `block target "…" not found` を stderr に出して exit 3 になる。`fail-<fixture 名>` を置くと exit 1 になる。
// `events` は events.jsonl を流して終わる (server の切断)。`events-hold` を置くと流した後も stream を開けておく。
// 呼び出しごとに引数を 1 つずつ single quote で囲って calls.log へ、環境変数を env.log へ追記する。
// --autostart=false が先頭に無い呼び出しは拒むので、成功を確かめるテストはどれも autostart を切っていることを確かめている。
func newFakeRex(t *testing.T) *fakeRex {
	t.Helper()
	dir := t.TempDir()
	return newFakeRexAt(t, dir, filepath.Join(dir, "rex"))
}

// newFakeRexAt は fixture を dir から読む fake の rex を bin に書く。dir と bin の親ディレクトリは作る。
func newFakeRexAt(t *testing.T, dir, bin string) *fakeRex {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("fixture の置き場の作成に失敗: %v", err)
	}
	script := `#!/bin/sh
d=` + shellQuote(dir) + `
env >> "$d/env.log"
for a in "$@"; do
  printf "'%s' " "$(printf '%s' "$a" | sed "s/'/'\\\\''/g")"
done >> "$d/calls.log"
echo >> "$d/calls.log"
if [ "$1" != "--autostart=false" ]; then
  echo "Error: --autostart=false がない" >&2
  exit 2
fi
shift
emit() {
  if [ -f "$d/fail-$1" ]; then
    echo "Error: server error" >&2
    exit 1
  elif [ -f "$d/$1" ]; then
    cat "$d/$1"
  else
    echo "Error: block target \"$2\" not found" >&2
    exit 3
  fi
}
case "$1" in
  ls) emit ls.json sessions ;;
  version) emit version.json version ;;
  block)
    case "$2" in
      ls) emit "block-ls-$4.json" "$4" ;;
      call) emit "$5-$4.json" "$4" ;;
      inspect) emit "inspect-$3.json" "$3" ;;
      *) echo "Error: unknown block subcommand $2" >&2; exit 2 ;;
    esac ;;
  capture) emit "capture-$3.txt" "$3" ;;
  run) emit run.json run ;;
  api) emit server-status.json server ;;
  events)
    emit events.jsonl events
    if [ -f "$d/events-hold" ]; then exec sleep 30; fi ;;
  send|send-key) emit "target-$3" "$3" ;;
  *) echo "Error: unknown command $1" >&2; exit 2 ;;
esac
`
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatalf("fake rex の置き場の作成に失敗: %v", err)
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("fake rex の作成に失敗: %v", err)
	}
	return &fakeRex{dir: dir, bin: bin}
}

func (f *fakeRex) put(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("fixture %s の作成に失敗: %v", name, err)
	}
}

func (f *fakeRex) putBlock(t *testing.T, blockID, programStatus, process string) {
	t.Helper()
	f.put(t, "program_status-"+blockID+".json", programStatus)
	f.put(t, "process-"+blockID+".json", process)
}

// assertCalls は rex に渡った引数列を、呼び出しごとに want と比べる。want は shell の語で書き、
// 空白を含む 1 引数は引用符で囲む。
func (f *fakeRex) assertCalls(t *testing.T, want ...string) {
	t.Helper()
	got := splitArgs(t, readLines(t, filepath.Join(f.dir, "calls.log")))
	if w := splitArgs(t, want); !slices.EqualFunc(got, w, slices.Equal[[]string]) {
		t.Errorf("calls =\n%q\nwant\n%q", got, w)
	}
}

func splitArgs(t *testing.T, lines []string) [][]string {
	t.Helper()
	out := make([][]string, 0, len(lines))
	for _, line := range lines {
		args, err := shellquote.Split(line)
		if err != nil {
			t.Fatalf("%q を引数に分けられない: %v", line, err)
		}
		out = append(out, args)
	}
	return out
}

func (f *fakeRex) client(resolve SessionResolver) *execClient {
	return &execClient{binPath: f.bin, timeout: noCommandTimeout, resolve: resolve}
}

// noCommandTimeout はタイムアウトを課さない。fake の shell 起動の遅れでテストが落ちないようにする。
const noCommandTimeout = 0

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- テストの一時ディレクトリ
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("%s の読み込みに失敗: %v", path, err)
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func staticResolver(m map[string]string) SessionResolver {
	return func() (map[string]string, error) { return m, nil }
}

// TestExecClient_PaneList は 2 session を回る 1 回の PaneList の配線を見る。session A に作業中の Claude と
// shell 以外の block、session B に block ls の後に閉じた block、program_status と process の間に閉じた block、
// 入力待ちの Claude を置く。
func TestExecClient_PaneList(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "ls.json", lsJSON(sessionA, sessionB))
	f.put(t, "block-ls-"+sessionA+".json", blockLsJSON(
		[2]string{blockClaudeWorking, blockstate.ShellFlavor},
		[2]string{blockNotShell, "com.superlogical.browser"},
	))
	f.put(t, "block-ls-"+sessionB+".json", blockLsJSON(
		[2]string{blockClosed, blockstate.ShellFlavor},
		[2]string{blockClosedMidway, blockstate.ShellFlavor},
		[2]string{blockClaudeDone, blockstate.ShellFlavor},
	))
	f.putBlock(t, blockClaudeWorking, measured(t, measuredOct10, "program_status-working.json"), processJSON(pidClaudeWorking, "claude", "/w/agents-daemon"))
	f.put(t, "program_status-"+blockClosedMidway+".json", psEmpty)
	f.putBlock(t, blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))

	resolves := 0
	c := f.client(func() (map[string]string, error) {
		resolves++
		return map[string]string{blockClaudeWorking: sidWorking, blockClaudeDone: sidDone}, nil
	})
	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v (閉じた block は読み飛ばすべき)", err)
	}

	t.Run("閉じた block を飛ばし session_id を埋める", func(t *testing.T) {
		// msg は fixture の実測値 (作業中の要約と、ターン終了の要約)。
		want := []multiplexer.Pane{
			{PaneID: blockClaudeWorking, CWD: "/w/agents-daemon", Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking, AgentMessage: "Showing rex CLI help for relevant subcommands", SessionID: sidWorking},
			{PaneID: blockClaudeDone, CWD: "/w/guard", Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "guard-gql-read の PR 作成待ち", SessionID: sidDone},
		}
		if !slices.Equal(panes, want) {
			t.Errorf("PaneList() =\n%+v\nwant\n%+v", panes, want)
		}
	})
	t.Run("shell の block にだけ method を打つ", func(t *testing.T) {
		f.assertCalls(t,
			"--autostart=false ls --json",
			"--autostart=false block ls -s "+sessionA+" --json",
			"--autostart=false block call -b "+blockClaudeWorking+" program_status",
			"--autostart=false block call -b "+blockClaudeWorking+" process",
			"--autostart=false block ls -s "+sessionB+" --json",
			"--autostart=false block call -b "+blockClosed+" program_status",
			"--autostart=false block call -b "+blockClosedMidway+" program_status",
			"--autostart=false block call -b "+blockClosedMidway+" process",
			"--autostart=false block call -b "+blockClaudeDone+" program_status",
			"--autostart=false block call -b "+blockClaudeDone+" process",
		)
	})
	t.Run("resolve は 1 回", func(t *testing.T) {
		if resolves != 1 {
			t.Errorf("resolve の呼び出し回数 = %d, want 1", resolves)
		}
	})
}

// not found 以外の失敗は block を飛ばさずエラーにする。
func TestExecClient_PaneList_FailureIsError(t *testing.T) {
	oneBlock := func(programStatus string) map[string]string {
		return map[string]string{
			"ls.json":                        lsJSON(sessionA),
			"block-ls-" + sessionA + ".json": blockLsJSON([2]string{blockClaudeWorking, blockstate.ShellFlavor}),
			"program_status-" + blockClaudeWorking + ".json": programStatus,
		}
	}
	failing := oneBlock(psEmpty)
	failing["fail-program_status-"+blockClaudeWorking+".json"] = ""
	tests := []struct {
		name     string
		fixtures map[string]string
		wantErr  string
	}{
		{"ls が失敗", nil, "not found"},
		{"block ls が失敗", map[string]string{"ls.json": lsJSON(sessionA)}, "not found"},
		{"block call が exit 3 以外で失敗", failing, "server error"},
		{"program_status が JSON でない", oneBlock("not json"), "応答の parse に失敗"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			for name, content := range tt.fixtures {
				f.put(t, name, content)
			}
			_, err := f.client(staticResolver(nil)).PaneList(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PaneList() error = %v, want %q を含む", err, tt.wantErr)
			}
		})
	}
}

func TestExecClient_DropsRexSessionFromChildEnv(t *testing.T) {
	t.Setenv("REX_SESSION", sessionA)
	t.Setenv("REX_BLOCK", blockClaudeWorking)
	t.Setenv("REX_FAKE_KEEP", "kept")
	f := newFakeRex(t)
	f.put(t, "server-status.json", measured(t, measuredOct10, "server_status.json"))

	if err := f.client(staticResolver(nil)).Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable() error = %v", err)
	}
	env := readLines(t, filepath.Join(f.dir, "env.log"))
	if len(env) == 0 {
		t.Fatal("env.log が空")
	}
	kept := false
	for _, line := range env {
		if strings.HasPrefix(line, "REX_SESSION=") || strings.HasPrefix(line, "REX_BLOCK=") {
			t.Errorf("子プロセスに渡った: %q", line)
		}
		if line == "REX_FAKE_KEEP=kept" {
			kept = true
		}
	}
	if !kept {
		t.Error("REX_SESSION / REX_BLOCK 以外の環境変数まで落ちている")
	}
}

func TestExecClient_PaneGet(t *testing.T) {
	f := newFakeRex(t)
	f.putBlock(t, blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w"))
	c := f.client(staticResolver(map[string]string{blockClaudeDone: sidDone}))

	got, err := c.PaneGet(context.Background(), blockClaudeDone)
	if err != nil {
		t.Fatalf("PaneGet() error = %v", err)
	}
	want := multiplexer.Pane{PaneID: blockClaudeDone, CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "guard-gql-read の PR 作成待ち", SessionID: sidDone}
	if got != want {
		t.Errorf("PaneGet() = %+v, want %+v", got, want)
	}
}

// 1 block を相手にする method は、block が無ければ rex の stderr を添えたエラーを返す。
func TestExecClient_TargetNotFoundIsError(t *testing.T) {
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
		{"SendText", func(c *execClient) error { return c.SendText(context.Background(), blockClosed, "続けて") }},
		{"SendKeys", func(c *execClient) error { return c.SendKeys(context.Background(), blockClosed, "enter") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(newFakeRex(t).client(staticResolver(nil)))
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Errorf("error = %v, want rex の stderr (not found) を含む", err)
			}
		})
	}
}

func TestExecClient_PaneRead(t *testing.T) {
	const screen = "line1\nline2\nline3\nline4\n"
	tests := []struct {
		name   string
		screen string
		lines  int
		want   string
	}{
		{"Lines 0 は全体", screen, 0, screen},
		{"末尾 2 行", screen, 2, "line3\nline4\n"},
		{"行数より多ければ全体", screen, 10, screen},
		{"末尾に改行が無ければ足さない", "a\nb\nc", 2, "b\nc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			f.put(t, "capture-"+blockClaudeWorking+".txt", tt.screen)
			got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking,
				multiplexer.PaneReadOptions{Lines: tt.lines})
			if err != nil {
				t.Fatalf("PaneRead() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PaneRead() = %q, want %q", got, tt.want)
			}
			f.assertCalls(t, "--autostart=false capture -b "+blockClaudeWorking+" --trim --unwrap")
		})
	}
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
			f := newFakeRex(t)
			f.put(t, "size-"+blockClaudeWorking+".json", measured(t, measuredOct20, "size.json"))
			f.put(t, "modes-"+blockClaudeWorking+".json", tt.modes)
			got, err := f.client(staticResolver(nil)).PaneGeometry(context.Background(), blockClaudeWorking)
			if err != nil {
				t.Fatalf("PaneGeometry() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PaneGeometry() = %+v, want %+v", got, tt.want)
			}
			f.assertCalls(t,
				"--autostart=false block call -b "+blockClaudeWorking+" size",
				"--autostart=false block call -b "+blockClaudeWorking+" modes",
			)
		})
	}

	t.Run("閉じた block は exit 3 の error", func(t *testing.T) {
		f := newFakeRex(t)
		_, err := f.client(staticResolver(nil)).PaneGeometry(context.Background(), blockClosed)
		if !isTargetNotFound(err) {
			t.Fatalf("PaneGeometry() error = %v, want exit 3", err)
		}
	})
}

func TestExecClient_SendTextAndKeys(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "target-"+blockClaudeDone, "")
	c := f.client(staticResolver(nil))

	// 文面は空白を含んでも 1 引数、キーは 1 つずつ別の引数で渡る。
	if err := c.SendText(context.Background(), blockClaudeDone, "続けて ください"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := c.SendKeys(context.Background(), blockClaudeDone, "esc", "enter"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	f.assertCalls(t,
		"--autostart=false send -b "+blockClaudeDone+" '続けて ください'",
		"--autostart=false send-key -b "+blockClaudeDone+" esc enter",
	)
}

func TestExecClient_Reachable(t *testing.T) {
	tests := []struct {
		name    string
		status  string // 空なら fixture を置かない (exit 3)
		wantErr bool
	}{
		{"pid があれば到達", measured(t, measuredOct10, "server_status.json"), false},
		{"pid 0 は到達でない", `{"pid":0}`, true},
		{"壊れた JSON", "not json", true},
		{"非 0 exit", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			if tt.status != "" {
				f.put(t, "server-status.json", tt.status)
			}
			err := f.client(staticResolver(nil)).Reachable(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Reachable() error = %v, wantErr %v", err, tt.wantErr)
			}
			f.assertCalls(t, "--autostart=false api call server.status")
		})
	}
}

func TestExecClient_Timeout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &execClient{binPath: bin, timeout: 50 * time.Millisecond, resolve: staticResolver(nil)}
	start := time.Now()
	err := c.Reachable(context.Background())
	if err == nil || !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("Reachable() error = %v, want タイムアウト", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed = %v, want タイムアウトで打ち切る", elapsed)
	}
}

func TestNewExecClient_BinPath(t *testing.T) {
	t.Setenv("REX_BIN_PATH", "/custom/rex")
	if c := NewExecClient(staticResolver(nil)).(*execClient); c.binPath != "/custom/rex" || c.timeout != 10*time.Second {
		t.Errorf("binPath = %q, timeout = %v, want /custom/rex, 10s", c.binPath, c.timeout)
	}
	// 未設定なら最初の呼び出しで PATH から引く (daemon の起動時点で rex が無くても Client は作れる)。
	t.Setenv("REX_BIN_PATH", "")
	if c := NewExecClient(staticResolver(nil)).(*execClient); c.binPath != "" {
		t.Errorf("binPath = %q, want 空 (未解決)", c.binPath)
	}
}
