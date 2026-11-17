package simulate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

func TestNewSimulateHost_前提チェック(t *testing.T) {
	t.Run("rex は REX_BLOCK が要る", func(t *testing.T) {
		env := map[string]string{"REX_SESSION": "session:01a1"}
		_, err := newSimulateHost(sessioncontext.MultiplexerKindRex, func(k string) string { return env[k] }, &fakeOps{})
		if err == nil || !strings.Contains(err.Error(), "REX_BLOCK") {
			t.Fatalf("newSimulateHost(rex) error = %v, want REX_BLOCK に触れるエラー", err)
		}
	})
	t.Run("rex は REX_BLOCK を anchor に持つ", func(t *testing.T) {
		env := map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:mine"}
		host, err := newSimulateHost(sessioncontext.MultiplexerKindRex, func(k string) string { return env[k] }, &fakeOps{})
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
	t.Run("pane を作れない client は error", func(t *testing.T) {
		env := map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:mine"}
		_, err := newSimulateHost(sessioncontext.MultiplexerKindRex, func(k string) string { return env[k] }, &multiplexerfake.Client{})
		if err == nil || !strings.Contains(err.Error(), "pane を作れない") {
			t.Fatalf("newSimulateHost(rex) error = %v, want pane を作れない error", err)
		}
	})
	t.Run("未対応の kind は error", func(t *testing.T) {
		_, err := newSimulateHost("tmux", func(string) string { return "" }, &fakeOps{})
		if err == nil || !strings.Contains(err.Error(), "未対応") {
			t.Fatalf("newSimulateHost(tmux) error = %v, want 未対応の error", err)
		}
	})
}

func TestSimulateScript(t *testing.T) {
	t.Run("rex: OSC 7501 を出してから画面を cat し cat -v で居座る", func(t *testing.T) {
		got := simulateScript(claudeProgramStatusCommand)
		// Claude Code 2.1.296 と同じ書式: OSC 7501 ; state=<state>:app=claude-code BEL。
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

// fakeOps は rex の Client が持つ pane の作成と後始末の口 (paneOps) を真似て、呼び出しを記録する。
type fakeOps struct {
	calls []string
}

func (o *fakeOps) SplitPane(_ context.Context, anchor, label string, argv []string) (string, error) {
	o.calls = append(o.calls, "split "+anchor+" "+label+" "+strings.Join(argv, " "))
	return "block:01a1-new", nil
}

func (o *fakeOps) ClosePane(_ context.Context, paneID string) error {
	o.calls = append(o.calls, "close "+paneID)
	return nil
}

func newRexHost() (*fakeOps, *rexHost) {
	ops := &fakeOps{}
	return ops, &rexHost{ops: ops, ownBlock: "block:mine"}
}

// SplitPane は自分の block の下に、script を shell を挟まずに走らせる block を作る。label は metadata の source。
func TestRexHost_SplitPane(t *testing.T) {
	ops, h := newRexHost()
	got, err := h.SplitPane(context.Background(), "/tmp/sim/simulate.sh", "/tmp/sim/screen.txt")
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	if got != "block:01a1-new" {
		t.Errorf("SplitPane() = %q, want block:01a1-new", got)
	}
	want := []string{"split block:mine " + simulateMetadataSource + " /bin/sh /tmp/sim/simulate.sh /tmp/sim/screen.txt"}
	if !slices.Equal(ops.calls, want) {
		t.Errorf("calls = %q, want %q", ops.calls, want)
	}
}

func TestRexHost_MarkAsClaudeAgentは外から何もしない(t *testing.T) {
	ops, h := newRexHost()
	if _, err := h.MarkAsClaudeAgent(context.Background(), "block:01a1-new"); err != nil {
		t.Fatalf("MarkAsClaudeAgent() error = %v", err)
	}
	if len(ops.calls) != 0 {
		t.Errorf("rex を呼んでいる: %v", ops.calls)
	}
	if h.ProgramStatusCommand() != claudeProgramStatusCommand {
		t.Errorf("ProgramStatusCommand() = %q", h.ProgramStatusCommand())
	}
}

func TestRexHost_ClosePane(t *testing.T) {
	ops, h := newRexHost()
	if err := h.ClosePane(context.Background(), "block:01a1-new"); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
	if !slices.Equal(ops.calls, []string{"close block:01a1-new"}) {
		t.Errorf("calls = %v, want close", ops.calls)
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
