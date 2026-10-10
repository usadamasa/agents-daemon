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
	t.Run("herdr は HERDR_ENV=1 が要る", func(t *testing.T) {
		_, err := newSimulateHost(sessionstate.MultiplexerKindHerdr, func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), "HERDR_ENV") {
			t.Fatalf("newSimulateHost(herdr) error = %v, want HERDR_ENV に触れるエラー", err)
		}
	})
}

// fakeRex は呼び出しを calls.log へ追記し､split にだけ fixture の JSON を返す fake の rex｡
type fakeRex struct {
	dir string
	bin string
}

func newFakeRex(t *testing.T, splitJSON string) *fakeRex {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d='` + dir + `'
printf '%s\n' "$*" >> "$d/calls.log"
if [ "$1" != "--autostart=false" ]; then
  echo "Error: --autostart=false がない" >&2
  exit 2
fi
shift
case "$1" in
  split) cat "$d/split.json" ;;
  send|send-key|block) ;;
  *) echo "Error: unknown command $1" >&2; exit 2 ;;
esac
`
	bin := filepath.Join(dir, "rex")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { // #nosec G306 -- テスト用の実行ファイル
		t.Fatalf("fake rex の作成に失敗: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "split.json"), []byte(splitJSON), 0o600); err != nil {
		t.Fatalf("fixture の作成に失敗: %v", err)
	}
	return &fakeRex{dir: dir, bin: bin}
}

func (f *fakeRex) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	if err != nil {
		t.Fatalf("calls.log の読み込みに失敗: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (f *fakeRex) host() *rexHost {
	return &rexHost{bin: f.bin, ownBlock: "block:mine", keyDelay: 0}
}

// 実測 (rex.md) の split --json の形｡API の new_split は block_ids / anchor_block_id / split_ids / revision を返す｡
const splitJSON = `{"anchor_block_id":"block:mine","block_ids":["block:01a1-new"],"split_ids":["split:01a1"],"revision":7,"window_id":"window:01a1"}`

func TestRexHost_SplitPane(t *testing.T) {
	f := newFakeRex(t, splitJSON)
	got, err := f.host().SplitPane(context.Background())
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
	for _, want := range []string{"--autostart=false", "split", "-b block:mine", "--focus=false", "--json"} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("split の引数に %q が無い: %s", want, calls[0])
		}
	}
	if strings.Contains(calls[0], " -- ") {
		t.Errorf("split にコマンドを渡している (shell のまま開く必要がある): %s", calls[0])
	}
}

func TestRexHost_SplitPane_block_idsが空なら失敗(t *testing.T) {
	f := newFakeRex(t, `{"anchor_block_id":"block:mine","block_ids":[],"split_ids":[],"revision":7}`)
	if _, err := f.host().SplitPane(context.Background()); err == nil {
		t.Fatal("SplitPane() error = nil, want block_ids が空のエラー")
	}
}

func TestRexHost_MarkAsClaudeAgent_OSC7501をシェルに打たせる(t *testing.T) {
	f := newFakeRex(t, splitJSON)
	if _, err := f.host().MarkAsClaudeAgent(context.Background(), "block:01a1-new"); err != nil {
		t.Fatalf("MarkAsClaudeAgent() error = %v", err)
	}
	calls := f.calls(t)
	if len(calls) != 2 {
		t.Fatalf("呼び出し = %v, want send と send-key の 2 回", calls)
	}
	if !strings.HasPrefix(calls[0], "--autostart=false send -b block:01a1-new ") {
		t.Errorf("1 回目 = %q, want send -b block:01a1-new …", calls[0])
	}
	// Claude Code 2.1.296 と同じ書式: OSC 7501 ; state=<state>:app=claude-code BEL｡
	// printf に解釈させる形で送り､生の ESC / BEL は pty に流さない｡
	if !strings.Contains(calls[0], `printf '\033]7501;state=idle:app=claude-code\a'`) {
		t.Errorf("1 回目に OSC 7501 の printf が無い: %q", calls[0])
	}
	if calls[1] != "--autostart=false send-key -b block:01a1-new enter" {
		t.Errorf("2 回目 = %q, want send-key enter", calls[1])
	}
}

func TestRexHost_WriteScreen_catをシェルに打たせる(t *testing.T) {
	f := newFakeRex(t, splitJSON)
	if err := f.host().WriteScreen(context.Background(), "block:01a1-new", "/tmp/dir with space/screen.txt"); err != nil {
		t.Fatalf("WriteScreen() error = %v", err)
	}
	calls := f.calls(t)
	if len(calls) != 2 {
		t.Fatalf("呼び出し = %v, want send と send-key の 2 回", calls)
	}
	if calls[0] != "--autostart=false send -b block:01a1-new cat '/tmp/dir with space/screen.txt'" {
		t.Errorf("1 回目 = %q, want cat をシングルクォートで", calls[0])
	}
	if calls[1] != "--autostart=false send-key -b block:01a1-new enter" {
		t.Errorf("2 回目 = %q, want send-key enter", calls[1])
	}
}

func TestRexHost_ClosePane(t *testing.T) {
	f := newFakeRex(t, splitJSON)
	h := f.host()
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
