package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

func TestNewSimulateHost_前提チェック(t *testing.T) {
	t.Run("rex は REX_BLOCK が要る", func(t *testing.T) {
		env := map[string]string{"REX_SESSION": "session:01a1"}
		_, err := newSimulateHost(sessionstate.MultiplexerKindRex, func(k string) string { return env[k] })
		if err == nil || !strings.Contains(err.Error(), "REX_BLOCK") {
			t.Fatalf("newSimulateHost(rex) error = %v, want REX_BLOCK に触れるエラー", err)
		}
	})
	t.Run("rex は REX_BLOCK を anchor に持つ", func(t *testing.T) {
		env := map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:mine"}
		host, err := newSimulateHost(sessionstate.MultiplexerKindRex, func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("newSimulateHost(rex) error = %v", err)
		}
		rh, ok := host.(*rexHost)
		if !ok {
			t.Fatalf("host = %T, want *rexHost", host)
		}
		if rh.ownBlock != "block:mine" {
			t.Errorf("ownBlock = %q, want block:mine", rh.ownBlock)
		}
	})
	t.Run("未対応の kind は error", func(t *testing.T) {
		_, err := newSimulateHost("tmux", func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), "未対応") {
			t.Fatalf("newSimulateHost(tmux) error = %v, want 未対応の error", err)
		}
	})
}

func TestSimulateScript(t *testing.T) {
	t.Run("rex: OSC 7501 を出してから画面を cat し cat -v で居座る", func(t *testing.T) {
		got := simulateScript(claudeProgramStatusCommand)
		// Claude Code 2.1.296 と同じ書式: OSC 7501 ; state=<state>:app=claude-code BEL｡
		wantOrder := []string{
			"#!/bin/sh\n",
			`printf '\033]7501;state=idle:app=claude-code\a'` + "\n",
			`/bin/cat "$1"` + "\n",
			"exec /bin/cat -v\n",
		}
		rest := got
		for _, want := range wantOrder {
			i := strings.Index(rest, want)
			if i < 0 {
				t.Fatalf("script に %q がこの順で無い:\n%s", want, got)
			}
			rest = rest[i+len(want):]
		}
	})
	t.Run("名乗るコマンドが空なら OSC 7501 の行は無い", func(t *testing.T) {
		got := simulateScript("")
		if strings.Contains(got, "7501") {
			t.Errorf("script に OSC 7501 がある:\n%s", got)
		}
		if !strings.Contains(got, "exec /bin/cat -v\n") {
			t.Errorf("受け手の cat -v が無い:\n%s", got)
		}
	})
}

// fakeCLI は呼び出しを calls.log へ追記し､サブコマンドごとの fixture (stdout) を返す fake の CLI｡
type fakeCLI struct {
	dir string
	bin string
}

// newFakeCLI は fixtures の key (引数の先頭の語を空白で繋いだもの) に一致する呼び出しにその内容を返す｡
// skipFirst は calls.log と fixture の照合から除く先頭の引数 (rex の --autostart=false)｡
func newFakeCLI(t *testing.T, name, skipFirst string, fixtures map[string]string) *fakeCLI {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d='` + dir + `'
printf '%s\n' "$*" >> "$d/calls.log"
`
	if skipFirst != "" {
		script += `if [ "$1" != '` + skipFirst + `' ]; then echo "Error: ` + skipFirst + ` がない" >&2; exit 2; fi
shift
`
	}
	script += `case "$1 $2" in
`
	for key, content := range fixtures {
		name := strings.ReplaceAll(key, " ", "-") + ".out"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("fixture の作成に失敗: %v", err)
		}
		script += `  "` + key + `"*) cat "$d/` + name + `" ;;` + "\n"
	}
	script += `  *) ;;
esac
`
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { // #nosec G306 -- テスト用の実行ファイル
		t.Fatalf("fake %s の作成に失敗: %v", name, err)
	}
	return &fakeCLI{dir: dir, bin: bin}
}

func (f *fakeCLI) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	if err != nil {
		t.Fatalf("calls.log の読み込みに失敗: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// 実測 (rex.md) の split --json の形｡API の new_split は block_ids / anchor_block_id / split_ids / revision を返す｡
const rexSplitJSON = `{"anchor_block_id":"block:mine","block_ids":["block:01a1-new"],"split_ids":["split:01a1"],"revision":7,"window_id":"window:01a1"}`

func newFakeRex(t *testing.T, splitJSON string) (*fakeCLI, *rexHost) {
	t.Helper()
	f := newFakeCLI(t, "rex", "--autostart=false", map[string]string{"split -b": splitJSON})
	return f, &rexHost{bin: f.bin, ownBlock: "block:mine"}
}

func TestRexHost_SplitPane(t *testing.T) {
	f, h := newFakeRex(t, rexSplitJSON)
	got, err := h.SplitPane(context.Background(), "/tmp/sim/simulate.sh", "/tmp/sim/screen.txt")
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	if got != "block:01a1-new" {
		t.Errorf("SplitPane() = %q, want block:01a1-new", got)
	}
	calls := f.calls(t)
	if len(calls) != 1 {
		t.Fatalf("呼び出し回数 = %d, want 1: %v", len(calls), calls)
	}
	for _, want := range []string{
		"--autostart=false", "split", "-b block:mine", "--focus=false", "--keep-open", "--shell=none", "--json",
		"-- /bin/sh /tmp/sim/simulate.sh /tmp/sim/screen.txt",
	} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("split の引数に %q が無い: %s", want, calls[0])
		}
	}
}

func TestRexHost_SplitPane_block_idsが空なら失敗(t *testing.T) {
	_, h := newFakeRex(t, `{"anchor_block_id":"block:mine","block_ids":[],"split_ids":[],"revision":7}`)
	if _, err := h.SplitPane(context.Background(), "/tmp/sim/simulate.sh", "/tmp/sim/screen.txt"); err == nil {
		t.Fatal("SplitPane() error = nil, want block_ids が空のエラー")
	}
}

func TestRexHost_MarkAsClaudeAgentは外から何もしない(t *testing.T) {
	f, h := newFakeRex(t, rexSplitJSON)
	if _, err := h.MarkAsClaudeAgent(context.Background(), "block:01a1-new"); err != nil {
		t.Fatalf("MarkAsClaudeAgent() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "calls.log")); err == nil {
		t.Errorf("rex を呼んでいる: %v", f.calls(t))
	}
	if h.ProgramStatusCommand() != claudeProgramStatusCommand {
		t.Errorf("ProgramStatusCommand() = %q", h.ProgramStatusCommand())
	}
}

func TestRexHost_ClosePane(t *testing.T) {
	f, h := newFakeRex(t, rexSplitJSON)
	if err := h.ClosePane(context.Background(), "block:01a1-new"); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
	calls := f.calls(t)
	if len(calls) != 1 || calls[0] != "--autostart=false block close block:01a1-new" {
		t.Errorf("呼び出し = %v, want block close", calls)
	}
	if got := h.CloseCommand("block:01a1-new"); got != "rex block close block:01a1-new" {
		t.Errorf("CloseCommand() = %q", got)
	}
}

func TestWaitForClaudeAgent(t *testing.T) {
	t.Run("Claude agent に見えた時点で返る", func(t *testing.T) {
		n := 0
		fake := &multiplexerfake.Client{
			PaneGetFunc: func(_ context.Context, paneID string) (multiplexer.Pane, error) {
				n++
				if n < 3 {
					return multiplexer.Pane{PaneID: paneID}, nil
				}
				return multiplexer.Pane{PaneID: paneID, Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle}, nil
			},
		}
		pane, err := waitForClaudeAgent(context.Background(), fake, "p1", 2*time.Second)
		if err != nil {
			t.Fatalf("waitForClaudeAgent() error = %v", err)
		}
		if pane.AgentStatus != multiplexer.AgentStatusIdle {
			t.Errorf("AgentStatus = %q, want idle", pane.AgentStatus)
		}
	})
	t.Run("期限まで見えなければ最後の観測を添えて失敗する", func(t *testing.T) {
		fake := &multiplexerfake.Client{
			PaneGetFunc: func(_ context.Context, paneID string) (multiplexer.Pane, error) {
				return multiplexer.Pane{PaneID: paneID}, nil
			},
		}
		_, err := waitForClaudeAgent(context.Background(), fake, "p1", 200*time.Millisecond)
		if err == nil {
			t.Fatal("waitForClaudeAgent() error = nil, want timeout")
		}
		if !strings.Contains(err.Error(), "agent=") {
			t.Errorf("error = %v, want 最後に観測した agent を含む", err)
		}
	})
	t.Run("PaneGet の失敗が続いても期限で失敗する", func(t *testing.T) {
		fake := &multiplexerfake.Client{
			PaneGetFunc: func(context.Context, string) (multiplexer.Pane, error) {
				return multiplexer.Pane{}, errors.New("block target not found")
			},
		}
		_, err := waitForClaudeAgent(context.Background(), fake, "p1", 200*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("waitForClaudeAgent() error = %v, want 最後のエラーを含む timeout", err)
		}
	})
}
