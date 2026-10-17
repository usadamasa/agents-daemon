package rexcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// 実測 (tmp/rex-measurements.md､rex 0.0.1791570264､Claude Code 2.1.296) の ID と JSON｡
const (
	sessionA = "session:01a12439-0839-7829-a4cb-aeac453570c1"
	sessionB = "session:01a1250f-c100-71e0-aed0-4a433569cf00"

	blockClaudeWorking = "block:01a12439-083a-74f9-9827-49c3478b2469"
	blockShell         = "block:01a1250f-c101-7290-9025-733c2a16aca7"
	blockClaudeDone    = "block:01a12540-cd47-7943-a503-91a06c58fab5"
	blockTrustDialog   = "block:01a12543-98e9-7288-8614-755491e41d4d"
	blockClosed        = "block:01a12599-0000-7000-8000-000000000001"
	blockNotShell      = "block:01a12599-0000-7000-8000-000000000002"

	sidWorking = "0d5e717f-82c5-4e71-ac74-799f7197e0de"
	sidDone    = "6b1f3c2a-1111-4222-8333-944455556666"
)

const lsJSON = `{"sessions":[{"session_id":"` + sessionA + `","label":"shy-alcove"},{"session_id":"` + sessionB + `","label":"hidden-pine"}]}`

func blockLsJSON(blocks ...[2]string) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, `{"block_id":"`+b[0]+`","label":"shell","creator_name":"com.superlogical.terminal","flavor":"`+b[1]+`","host_id":"dfd31eaf-0189-45ad-9a36-10c09f3cb111","placement":"placed","window_id":"window:01a1250f-c11a-7a87-b4b5-eca09f61b728"}`)
	}
	return `{"blocks":[` + strings.Join(parts, ",") + `]}`
}

const (
	psWorking = `{"records":[` +
		`{"state":"working","app":"claude-code","effective_app":"claude-code","msg":"Showing rex CLI help for relevant subcommands","updated_at":"2026-10-10T18:30:19.629038+09:00","owner":{"pid":15470,"name":"2.1.296"}},` +
		`{"id":"a01b73b4e3800b31f","state":"done","app":"claude-code","effective_app":"claude-code","title":"Map herdrcli and its consumers","updated_at":"2026-10-10T18:29:58.836427+09:00","owner":{"pid":15470,"name":"2.1.296"}}]}`
	psDone        = `{"records":[{"state":"done","app":"claude-code","effective_app":"claude-code","msg":"guard-gql-read の PR 作成待ち","updated_at":"2026-10-10T18:16:09.305373+09:00","owner":{"pid":17633,"name":"2.1.296"}}]}`
	psTrustDialog = `{"records":[{"state":"blocked","app":"claude","effective_app":"claude","updated_at":"2026-10-10T18:47:01.189802+09:00","owner":{"pid":89100,"name":"2.1.296"}}]}`
	psPermission  = `{"records":[{"state":"blocked","kind":"permission","app":"claude-code","effective_app":"claude-code","msg":"approve Bash: rm -rf ./tmp/junk","updated_at":"2026-10-10T18:51:48.057417+09:00","owner":{"pid":94252,"name":"2.1.296"}}]}`
	psIdle        = `{"records":[{"state":"idle","app":"claude-code","effective_app":"claude-code","updated_at":"2026-10-10T18:52:20.000000+09:00","owner":{"pid":94252,"name":"2.1.296"}}]}`
	psEmpty       = `{"records":[]}`
	// event payload の root record は "id":"" で来る｡
	psEmptyID      = `{"records":[{"id":"","state":"working","app":"claude-code","updated_at":"2026-10-10T18:30:41.644437+09:00"}]}`
	psSubagentOnly = `{"records":[{"id":"a01b73b4e3800b31f","state":"working","app":"claude-code","updated_at":"2026-10-10T18:29:58.836427+09:00"}]}`
	psUnknownState = `{"records":[{"state":"starting","app":"claude-code","updated_at":"2026-10-10T18:29:58.836427+09:00"}]}`
)

func processJSON(argv0, cwd string) string {
	return `{"last_exit":null,` +
		`"child":{"pid":33929,"user_id":0,"user":"root","name":"login","path":"/usr/bin/login"},` +
		`"foreground":{"pid":15470,"user_id":501,"user":"usadamasa","name":"2.1.296","path":"/Users/usadamasa/.local/share/claude/versions/2.1.296","invoked_path":"/Users/usadamasa/.local/bin/claude","argv0":"` + argv0 + `","cwd":"` + cwd + `"},` +
		`"ancestors":[{"pid":33930,"user_id":501,"user":"usadamasa","name":"zsh","path":"/bin/zsh","invoked_path":"/bin/zsh","argv0":"-zsh","cwd":"` + cwd + `"}],` +
		`"home":"/Users/usadamasa"}`
}

const serverStatusJSON = `{"pid":46997,"instance_id":"a842cc30-1b60-4180-862a-1d2843681d21","host_id":"dfd31eaf-0189-45ad-9a36-10c09f3cb111","started_at":"2026-10-10T02:13:17.525033Z","exit_empty":false,"session_count":2,"tailscale":{"state":"disabled","allow_tailnet_members":false},"config":{"generation":1,"hash":""}}`

// fakeRex は fixture のディレクトリと､それを読む fake の rex｡
type fakeRex struct {
	dir string
	bin string
}

// newFakeRex は引数で fixture を選んで返す fake の rex を書く｡fixture が無ければ実機と同じく
// `block target "…" not found` を stderr に出して exit 3 になる｡呼び出しごとに引数を
// calls.log へ､環境変数を env.log へ追記する｡--autostart=false が先頭に無い呼び出しは拒む｡
func newFakeRex(t *testing.T) *fakeRex {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d=` + shellQuote(dir) + `
env >> "$d/env.log"
printf '%s\n' "$*" >> "$d/calls.log"
if [ "$1" != "--autostart=false" ]; then
  echo "Error: --autostart=false がない" >&2
  exit 2
fi
shift
emit() {
  if [ -f "$d/$1" ]; then
    cat "$d/$1"
  else
    echo "Error: block target \"$2\" not found" >&2
    exit 3
  fi
}
case "$1" in
  ls) emit ls.json sessions ;;
  block)
    case "$2" in
      ls) emit "block-ls-$4.json" "$4" ;;
      call) emit "$5-$4.json" "$4" ;;
      *) echo "Error: unknown block subcommand $2" >&2; exit 2 ;;
    esac ;;
  capture) emit "capture-$3.txt" "$3" ;;
  api) emit server-status.json server ;;
  send|send-key) emit "target-$3" "$3" ;;
  *) echo "Error: unknown command $1" >&2; exit 2 ;;
esac
`
	bin := filepath.Join(dir, "rex")
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

func (f *fakeRex) calls(t *testing.T) []string {
	t.Helper()
	return readLines(t, filepath.Join(f.dir, "calls.log"))
}

func (f *fakeRex) client(resolve multiplexer.SessionResolver) *execClient {
	return &execClient{binPath: f.bin, timeout: noCommandTimeout, resolve: resolve}
}

// noCommandTimeout はタイムアウトを課さない｡fake の shell 起動の遅れでテストが落ちないようにする｡
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

func staticResolver(m map[string]string) multiplexer.SessionResolver {
	return func() (map[string]string, error) { return m, nil }
}

// setupTwoSessions は 2 session の実測に近い構成を作る｡session A に作業中の Claude と shell だけの block､
// session B に入力待ちの Claude､trust dialog の Claude､block ls の後に閉じた block､shell 以外の block｡
func setupTwoSessions(t *testing.T) *fakeRex {
	t.Helper()
	f := newFakeRex(t)
	f.put(t, "ls.json", lsJSON)
	f.put(t, "block-ls-"+sessionA+".json", blockLsJSON(
		[2]string{blockClaudeWorking, shellFlavor},
		[2]string{blockShell, shellFlavor},
	))
	f.put(t, "block-ls-"+sessionB+".json", blockLsJSON(
		[2]string{blockClaudeDone, shellFlavor},
		[2]string{blockTrustDialog, shellFlavor},
		[2]string{blockClosed, shellFlavor},
		[2]string{blockNotShell, "com.superlogical.browser"},
	))
	f.putBlock(t, blockClaudeWorking, psWorking, processJSON("claude", "/Users/usadamasa/src/github.com/usadamasa/agents-daemon"))
	f.putBlock(t, blockShell, psEmpty, processJSON("-zsh", "/Users/usadamasa"))
	f.putBlock(t, blockClaudeDone, psDone, processJSON("claude", "/Users/usadamasa/src/github.com/usadamasa/guard"))
	f.putBlock(t, blockTrustDialog, psTrustDialog, processJSON("claude", "/Users/usadamasa/tmp/new"))
	return f
}

func TestExecClient_PaneList_TwoSessions(t *testing.T) {
	f := setupTwoSessions(t)
	c := f.client(staticResolver(map[string]string{
		blockClaudeWorking: sidWorking,
		blockClaudeDone:    sidDone,
	}))

	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}

	want := []multiplexer.Pane{
		{PaneID: blockClaudeWorking, TerminalID: blockClaudeWorking, CWD: "/Users/usadamasa/src/github.com/usadamasa/agents-daemon", Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking, SessionID: sidWorking},
		{PaneID: blockShell, TerminalID: blockShell, CWD: "/Users/usadamasa", AgentStatus: multiplexer.AgentStatusUnknown},
		{PaneID: blockClaudeDone, TerminalID: blockClaudeDone, CWD: "/Users/usadamasa/src/github.com/usadamasa/guard", Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, SessionID: sidDone},
		{PaneID: blockTrustDialog, TerminalID: blockTrustDialog, CWD: "/Users/usadamasa/tmp/new", Agent: "claude", AgentStatus: multiplexer.AgentStatusBlocked},
	}
	if !slices.Equal(panes, want) {
		t.Errorf("PaneList() =\n%+v\nwant\n%+v", panes, want)
	}
	if multiplexer.IsClaudeAgent(panes[1]) {
		t.Errorf("shell だけの block を Claude と判定した: %+v", panes[1])
	}
}

func TestExecClient_PaneList_CallsOnlyShellBlocks(t *testing.T) {
	f := setupTwoSessions(t)
	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	for _, call := range f.calls(t) {
		if strings.Contains(call, blockNotShell) {
			t.Errorf("shell 以外の block に method を打った: %q", call)
		}
	}
}

func TestExecClient_PaneList_EveryCallDisablesAutostart(t *testing.T) {
	f := setupTwoSessions(t)
	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	calls := f.calls(t)
	if len(calls) == 0 {
		t.Fatal("rex が 1 回も呼ばれていない")
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "--autostart=false ") {
			t.Errorf("--autostart=false が先頭に無い呼び出し: %q", call)
		}
	}
}

func TestExecClient_PaneList_ArgsOfEachCall(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "ls.json", `{"sessions":[{"session_id":"`+sessionA+`","label":"shy-alcove"}]}`)
	f.put(t, "block-ls-"+sessionA+".json", blockLsJSON([2]string{blockClaudeWorking, shellFlavor}))
	f.putBlock(t, blockClaudeWorking, psWorking, processJSON("claude", "/x"))

	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	want := []string{
		"--autostart=false ls --json",
		"--autostart=false block ls -s " + sessionA + " --json",
		"--autostart=false block call -b " + blockClaudeWorking + " program_status",
		"--autostart=false block call -b " + blockClaudeWorking + " process",
	}
	if got := f.calls(t); !slices.Equal(got, want) {
		t.Errorf("calls =\n%v\nwant\n%v", got, want)
	}
}

func TestExecClient_PaneList_SkipsBlockClosedAfterBlockLs(t *testing.T) {
	f := setupTwoSessions(t)
	panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v (閉じた block は読み飛ばすべき)", err)
	}
	for _, p := range panes {
		if p.PaneID == blockClosed {
			t.Errorf("閉じた block が PaneList に残った: %+v", p)
		}
	}
	if len(panes) != 4 {
		t.Errorf("len(panes) = %d, want 4 (閉じた block の前後の block は残る)", len(panes))
	}
}

func TestExecClient_PaneList_SkipsBlockClosedBetweenMethods(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "ls.json", `{"sessions":[{"session_id":"`+sessionA+`","label":"shy-alcove"}]}`)
	f.put(t, "block-ls-"+sessionA+".json", blockLsJSON(
		[2]string{blockClosed, shellFlavor},
		[2]string{blockClaudeWorking, shellFlavor},
	))
	// program_status は返ったが process の前に閉じた｡
	f.put(t, "program_status-"+blockClosed+".json", psDone)
	f.putBlock(t, blockClaudeWorking, psWorking, processJSON("claude", "/x"))

	panes, err := f.client(staticResolver(nil)).PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	if len(panes) != 1 || panes[0].PaneID != blockClaudeWorking {
		t.Errorf("PaneList() = %+v, want 作業中の block だけ", panes)
	}
}

func TestExecClient_PaneList_LsFailureIsError(t *testing.T) {
	f := newFakeRex(t)
	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err == nil {
		t.Fatal("PaneList() error = nil, want ls の失敗")
	}
}

func TestExecClient_PaneList_BlockLsFailureIsError(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "ls.json", lsJSON)
	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err == nil {
		t.Fatal("PaneList() error = nil, want block ls の失敗")
	}
}

func TestExecClient_PaneList_NonNotFoundBlockFailureIsError(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "ls.json", `{"sessions":[{"session_id":"`+sessionA+`","label":"shy-alcove"}]}`)
	f.put(t, "block-ls-"+sessionA+".json", blockLsJSON([2]string{blockClaudeWorking, shellFlavor}))
	f.putBlock(t, blockClaudeWorking, "not json", processJSON("claude", "/x"))
	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err == nil {
		t.Fatal("PaneList() error = nil, want program_status の parse 失敗")
	}
}

func TestExecClient_PaneList_ResolverCalledOnce(t *testing.T) {
	f := setupTwoSessions(t)
	n := 0
	c := f.client(func() (map[string]string, error) { n++; return nil, nil })
	if _, err := c.PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	if n != 1 {
		t.Errorf("resolve の呼び出し回数 = %d, want 1", n)
	}
}

func TestExecClient_DropsRexSessionFromChildEnv(t *testing.T) {
	t.Setenv("REX_SESSION", sessionA)
	t.Setenv("REX_BLOCK", blockClaudeWorking)
	t.Setenv("REX_FAKE_KEEP", "kept")
	f := setupTwoSessions(t)

	if _, err := f.client(staticResolver(nil)).PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
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

func TestToPane(t *testing.T) {
	tests := []struct {
		name       string
		ps         string
		argv0      string
		wantAgent  string
		wantStatus multiplexer.AgentStatus
	}{
		{"作業中の claude-code", psWorking, "claude", "claude", multiplexer.AgentStatusWorking},
		{"ターン終了の done", psDone, "claude", "claude", multiplexer.AgentStatusDone},
		{"trust dialog は app=claude で blocked", psTrustDialog, "2.1.296", "claude", multiplexer.AgentStatusBlocked},
		{"permission prompt は blocked", psPermission, "claude", "claude", multiplexer.AgentStatusBlocked},
		{"permission を断った後の idle", psIdle, "claude", "claude", multiplexer.AgentStatusIdle},
		{"event 形の id 空の root record", psEmptyID, "claude", "claude", multiplexer.AgentStatusWorking},
		{"未知の state は unknown", psUnknownState, "claude", "claude", multiplexer.AgentStatusUnknown},
		{"record 無しでも argv0 が claude なら Claude", psEmpty, "claude", "claude", multiplexer.AgentStatusUnknown},
		{"shell だけの block", psEmpty, "-zsh", "", multiplexer.AgentStatusUnknown},
		{"subagent の record は見ない", psSubagentOnly, "-zsh", "", multiplexer.AgentStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ps programStatusResult
			if err := json.Unmarshal([]byte(tt.ps), &ps); err != nil {
				t.Fatalf("program_status の parse に失敗: %v", err)
			}
			var proc processResult
			if err := json.Unmarshal([]byte(processJSON(tt.argv0, "/w")), &proc); err != nil {
				t.Fatalf("process の parse に失敗: %v", err)
			}
			got := toPane(blockClaudeWorking, ps, proc, "sid")
			want := multiplexer.Pane{
				PaneID: blockClaudeWorking, TerminalID: blockClaudeWorking, CWD: "/w",
				Agent: tt.wantAgent, AgentStatus: tt.wantStatus, SessionID: "sid",
			}
			if got != want {
				t.Errorf("toPane() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestExecClient_PaneGet(t *testing.T) {
	f := newFakeRex(t)
	f.putBlock(t, blockClaudeDone, psDone, processJSON("claude", "/w"))
	c := f.client(staticResolver(map[string]string{blockClaudeDone: sidDone}))

	got, err := c.PaneGet(context.Background(), blockClaudeDone)
	if err != nil {
		t.Fatalf("PaneGet() error = %v", err)
	}
	want := multiplexer.Pane{PaneID: blockClaudeDone, TerminalID: blockClaudeDone, CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, SessionID: sidDone}
	if got != want {
		t.Errorf("PaneGet() = %+v, want %+v", got, want)
	}
}

func TestExecClient_PaneGet_MissingBlockIsError(t *testing.T) {
	f := newFakeRex(t)
	_, err := f.client(staticResolver(nil)).PaneGet(context.Background(), blockClosed)
	if err == nil {
		t.Fatal("PaneGet() error = nil, want not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want rex の stderr を含む", err)
	}
}

func TestExecClient_PaneRead(t *testing.T) {
	screen := "line1\nline2\nline3\nline4\n"
	tests := []struct {
		name  string
		lines int
		want  string
	}{
		{"Lines 0 は全体", 0, screen},
		{"末尾 2 行", 2, "line3\nline4\n"},
		{"行数より多ければ全体", 10, screen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			f.put(t, "capture-"+blockClaudeWorking+".txt", screen)
			got, err := f.client(staticResolver(nil)).PaneRead(context.Background(), blockClaudeWorking,
				multiplexer.PaneReadOptions{Lines: tt.lines})
			if err != nil {
				t.Fatalf("PaneRead() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("PaneRead() = %q, want %q", got, tt.want)
			}
			wantCall := []string{"--autostart=false capture -b " + blockClaudeWorking + " --trim --unwrap"}
			if calls := f.calls(t); !slices.Equal(calls, wantCall) {
				t.Errorf("calls = %v, want %v", calls, wantCall)
			}
		})
	}
}

func TestTailLines_NoTrailingNewline(t *testing.T) {
	if got := tailLines("a\nb\nc", 2); got != "b\nc" {
		t.Errorf("tailLines() = %q, want %q", got, "b\nc")
	}
}

func TestExecClient_SendTextAndKeys(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "target-"+blockClaudeDone, "")
	c := f.client(staticResolver(nil))

	if err := c.SendText(context.Background(), blockClaudeDone, "続けて"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := c.SendKeys(context.Background(), blockClaudeDone, "esc", "enter"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	want := []string{
		"--autostart=false send -b " + blockClaudeDone + " 続けて",
		"--autostart=false send-key -b " + blockClaudeDone + " esc enter",
	}
	if got := f.calls(t); !slices.Equal(got, want) {
		t.Errorf("calls =\n%v\nwant\n%v", got, want)
	}
}

func TestExecClient_SendKeys_MissingBlockIsError(t *testing.T) {
	f := newFakeRex(t)
	if err := f.client(staticResolver(nil)).SendKeys(context.Background(), blockClosed, "enter"); err == nil {
		t.Fatal("SendKeys() error = nil, want not found")
	}
}

func TestExecClient_Reachable(t *testing.T) {
	tests := []struct {
		name    string
		status  string // 空なら fixture を置かない (exit 3)
		wantErr bool
	}{
		{"pid があれば到達", serverStatusJSON, false},
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
	if c := NewExecClient(staticResolver(nil)).(*execClient); c.binPath != "/custom/rex" {
		t.Errorf("binPath = %q, want /custom/rex", c.binPath)
	}
	t.Setenv("REX_BIN_PATH", "")
	if c := NewExecClient(staticResolver(nil)).(*execClient); c.binPath != "rex" {
		t.Errorf("binPath = %q, want rex", c.binPath)
	}
}
