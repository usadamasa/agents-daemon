package rexcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/cli"
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
  session)
    case "$2" in
      new) emit session-new.json session ;;
      kill) exit 0 ;;
      *) echo "Error: unknown session subcommand $2" >&2; exit 2 ;;
    esac ;;
  window)
    case "$2" in
      new) emit window-new.json session ;;
      *) echo "Error: unknown window subcommand $2" >&2; exit 2 ;;
    esac ;;
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
	return &execClient{cli: cli.New(f.bin, noCommandTimeout), timeout: noCommandTimeout, resolve: resolve}
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
// shell 以外の block、session B に list_blocks の後に閉じた block、program_status と process の間に閉じた block、
// 入力待ちの Claude を置く。
func TestExecClient_PaneList(t *testing.T) {
	f := newFakeAPI(t)
	f.addSession(sessionA,
		[2]string{blockClaudeWorking, blockstate.ShellFlavor},
		[2]string{blockNotShell, "com.superlogical.browser"},
	)
	f.addSession(sessionB,
		[2]string{blockClosed, blockstate.ShellFlavor},
		[2]string{blockClosedMidway, blockstate.ShellFlavor},
		[2]string{blockClaudeDone, blockstate.ShellFlavor},
	)
	f.putBlock(blockClaudeWorking, measured(t, measuredOct10, "program_status-working.json"), processJSON(pidClaudeWorking, "claude", "/w/agents-daemon"))
	f.put("program_status-"+blockClosedMidway, psEmpty)
	f.putBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))

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
			{PaneID: blockClaudeWorking, CWD: "/w/agents-daemon", PID: pidClaudeWorking, Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking, AgentMessage: "Showing rex CLI help for relevant subcommands", SessionID: sidWorking},
			{PaneID: blockClaudeDone, CWD: "/w/guard", PID: pidClaudeDone, Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "guard-gql-read の PR 作成待ち", SessionID: sidDone},
		}
		if !slices.Equal(panes, want) {
			t.Errorf("PaneList() =\n%+v\nwant\n%+v", panes, want)
		}
	})
	t.Run("shell の block にだけ method を打つ", func(t *testing.T) {
		f.assertCalls(t,
			"session.list",
			"session.list_blocks "+sessionA,
			"program_status "+blockClaudeWorking,
			"process "+blockClaudeWorking,
			"session.list_blocks "+sessionB,
			"program_status "+blockClosed,
			"program_status "+blockClosedMidway,
			"process "+blockClosedMidway,
			"program_status "+blockClaudeDone,
			"process "+blockClaudeDone,
		)
	})
	t.Run("block の method は block の session を名指す", func(t *testing.T) {
		for _, p := range f.requestsOf(terminalMethod + "process") {
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
	f.listOnly(sessionA)
	f.addSession(sessionB, [2]string{blockClaudeDone, blockstate.ShellFlavor})
	f.putBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w/guard"))

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
		"program_status "+blockClaudeDone,
		"process "+blockClaudeDone,
	)
}

// not found 以外の失敗は block を飛ばさずエラーにする。
func TestExecClient_PaneList_FailureIsError(t *testing.T) {
	oneBlock := func(f *fakeAPI, programStatus string) {
		f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
		f.put("program_status-"+blockClaudeWorking, programStatus)
	}
	tests := []struct {
		name    string
		setup   func(f *fakeAPI)
		wantErr string
	}{
		{"list_blocks が not found 以外で失敗", func(f *fakeAPI) {
			f.addSession(sessionA)
			f.fail("list_blocks-" + sessionA)
		}, "server error"},
		{"block の method が not found 以外で失敗", func(f *fakeAPI) {
			oneBlock(f, psEmpty)
			f.fail("program_status-" + blockClaudeWorking)
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
		c := &execClient{resolve: staticResolver(nil), socket: fixedSocket(t.TempDir() + "/none.sock")}
		if _, err := c.PaneList(context.Background()); err == nil {
			t.Fatal("繋がらないのに PaneList が成功した")
		}
	})
}

func TestExecClient_DropsRexSessionFromChildEnv(t *testing.T) {
	t.Setenv("REX_SESSION", sessionA)
	t.Setenv("REX_BLOCK", blockClaudeWorking)
	t.Setenv("REX_FAKE_KEEP", "kept")
	f := newFakeRex(t)

	if err := f.client(staticResolver(nil)).SessionKill(context.Background(), sessionA); err != nil {
		t.Fatalf("SessionKill() error = %v", err)
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

// PaneGet は block の session を target.resolve で引いてから block の method を打つ (block の method は session_id が要る)。
func TestExecClient_PaneGet(t *testing.T) {
	f := newFakeAPI(t)
	f.addSession(sessionB, [2]string{blockClaudeDone, blockstate.ShellFlavor})
	f.putBlock(blockClaudeDone, measured(t, measuredOct10, "program_status-done.json"), processJSON(pidClaudeDone, "claude", "/w"))
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
	if err := json.Unmarshal(f.requestsOf("target.resolve")[0], &req); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(req.Kinds, []string{"block"}) || req.Context != nil {
		t.Errorf("target.resolve の kinds = %v, context = %s", req.Kinds, req.Context)
	}
}

// API で読む method は、block が無ければ not found の error を返す (target.resolve が見つけられない)。
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
	f.addSession(sessionB, [2]string{blockClosedMidway, blockstate.ShellFlavor})
	_, err := f.client(staticResolver(nil)).PaneGet(context.Background(), blockClosedMidway)
	if !isNotFound(err) {
		t.Errorf("PaneGet() error = %v, want not found", err)
	}
}

// CLI で送る method は、block が無ければ rex の stderr を添えたエラーを返す。
func TestExecClient_TargetNotFoundIsError(t *testing.T) {
	tests := []struct {
		name string
		call func(c *execClient) error
	}{
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

// PaneRead は format の content に改行を 1 つ足して返す。rex の CLI の capture は出力の末尾に改行を足し、
// 末尾の n 行を切り出す tailLines と呼び手はその形を前提にしている (2026-11-17 に実機で capture と format を
// バイト単位で比べた)。
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeAPI(t)
			f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
			f.put("format-"+blockClaudeWorking, tt.content)
			got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking,
				multiplexer.PaneReadOptions{Lines: tt.lines})
			if err != nil {
				t.Fatalf("PaneRead() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PaneRead() = %q, want %q", got, tt.want)
			}
			f.assertCalls(t, "target.resolve "+blockClaudeWorking, "format "+blockClaudeWorking)
			assertFormatArgs(t, f, `{"trim":true,"unwrap":true}`)
		})
	}
}

// assertFormatArgs は最後の format の args を want (JSON) と比べる。CLI の capture --trim --unwrap と同じ args になる。
func assertFormatArgs(t *testing.T, f *fakeAPI, want string) {
	t.Helper()
	reqs := f.requestsOf(terminalMethod + "format")
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
	f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
	f.put("format-"+blockClaudeWorking, "❯ \x1b[2mhint\x1b[0m  ")
	got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking,
		multiplexer.PaneReadOptions{Styled: true})
	if err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if got != "❯ \x1b[2mhint\x1b[0m  \n" {
		t.Errorf("PaneRead() = %q", got)
	}
	assertFormatArgs(t, f, `{"trim":true,"unwrap":true,"format":"vt"}`)
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
			f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
			f.put("size-"+blockClaudeWorking, measured(t, measuredOct20, "size.json"))
			f.put("modes-"+blockClaudeWorking, tt.modes)
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
			f.setStatus(tt.status)
			err := f.client(staticResolver(nil)).Reachable(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Reachable() error = %v, wantErr %v", err, tt.wantErr)
			}
			f.assertCalls(t, "server.status")
		})
	}
	t.Run("server に繋がらない", func(t *testing.T) {
		c := &execClient{resolve: staticResolver(nil), socket: fixedSocket(t.TempDir() + "/none.sock")}
		if err := c.Reachable(context.Background()); err == nil {
			t.Fatal("繋がらないのに Reachable が成功した")
		}
	})
}

// 接続は 1 本を使い回し、張った直後に session.attach_all を打つ (attach していないと session を名指す method が
// conflict になる)。購読はまだ CLI なので、event は block_event を含まない session の範囲に絞る。
func TestExecClient_ReusesOneConnection(t *testing.T) {
	f := newFakeAPI(t)
	f.setStatus(`{"pid":1}`)
	f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
	f.put("format-"+blockClaudeWorking, "x")
	c := f.client(staticResolver(nil))
	for range 3 {
		if err := c.Reachable(context.Background()); err != nil {
			t.Fatalf("Reachable() error = %v", err)
		}
	}
	if _, err := c.PaneRead(context.Background(), blockClaudeWorking, multiplexer.PaneReadOptions{}); err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if n := len(f.srv.Hellos()); n != 1 {
		t.Errorf("接続を %d 回張った (1 回のはず)", n)
	}
	attaches := f.requestsOf("session.attach_all")
	if len(attaches) != 1 || string(attaches[0]) != `{"event_scope":"session"}` {
		t.Errorf("attach_all = %s", attaches)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	eventually(t, "server の接続が閉じる", func() bool { return f.srv.Connections() == 0 })
}

// server が切れたら次の呼び出しで張り直す (rex の server の再起動、更新)。
func TestExecClient_RedialsAfterDisconnect(t *testing.T) {
	f := newFakeAPI(t)
	f.setStatus(`{"pid":1}`)
	c := f.client(staticResolver(nil))
	if err := c.Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable() error = %v", err)
	}
	f.srv.CloseConnections()
	eventually(t, "server の接続が閉じる", func() bool { return f.srv.Connections() == 0 })
	eventually(t, "張り直した Reachable が通る", func() bool { return c.Reachable(context.Background()) == nil })
	if n := len(f.srv.Hellos()); n != 2 {
		t.Errorf("接続を %d 回張った (2 回のはず)", n)
	}
}

// 応答が返らない request は Client の timeout で打ち切る。
func TestExecClient_APITimeout(t *testing.T) {
	f := newFakeAPI(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f.srv.Handle("server.status", func(json.RawMessage) (any, error) { <-block; return nil, nil })
	c := f.client(staticResolver(nil))
	c.timeout = 50 * time.Millisecond
	start := time.Now()
	err := c.Reachable(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Reachable() error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed = %v, want タイムアウトで打ち切る", elapsed)
	}
}

func TestExecClient_Timeout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &execClient{cli: cli.New(bin, 50*time.Millisecond), resolve: staticResolver(nil)}
	start := time.Now()
	err := c.SessionKill(context.Background(), sessionA)
	if err == nil || !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("SessionKill() error = %v, want タイムアウト", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed = %v, want タイムアウトで打ち切る", elapsed)
	}
}

func TestNewExecClient_BinPath(t *testing.T) {
	t.Setenv("REX_BIN_PATH", "/custom/rex")
	if c := NewExecClient(staticResolver(nil)).(*execClient); c.cli.BinPath() != "/custom/rex" || c.timeout != 10*time.Second {
		t.Errorf("binPath = %q, timeout = %v, want /custom/rex, 10s", c.cli.BinPath(), c.timeout)
	}
	// 未設定なら最初の呼び出しで PATH から引く (daemon の起動時点で rex が無くても Client は作れる)。
	t.Setenv("REX_BIN_PATH", "")
	if c := NewExecClient(staticResolver(nil)).(*execClient); c.cli.BinPath() != "" {
		t.Errorf("binPath = %q, want 空 (未解決)", c.cli.BinPath())
	}
}

// NewExecClient は REX_SERVER から socket を引く。rex の外 (REX_SERVER が無い) では既定の場所を使う。
func TestNewExecClient_Socket(t *testing.T) {
	t.Setenv("REX_SERVER", "unix:///tmp/x%20y/server.sock")
	got, err := NewExecClient(staticResolver(nil)).(*execClient).socket()
	if err != nil || got != "/tmp/x y/server.sock" {
		t.Errorf("socket = %q, %v", got, err)
	}
}
