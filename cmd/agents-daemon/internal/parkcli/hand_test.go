package parkcli

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
)

// paneRepo は status.json の pane からセッションの cwd を引き、メモの repo と同じ鍵 (key) にする。
func TestPaneRepo(t *testing.T) {
	snap := &daemonstate.StatusSnapshot{Panes: []daemonstate.PaneStatus{
		{PaneID: "b1", CWD: "/src/a"},
		{PaneID: "b2", SessionID: "s1", CWD: "/src/a/.claude/worktrees/w"},
	}}
	key := func(cwd string) string {
		if cwd == "/src/a/.claude/worktrees/w" {
			return "/src/a"
		}
		return "unexpected:" + cwd
	}
	if got := paneRepo(snap, "s1", key); got != "/src/a" {
		t.Errorf("paneRepo(s1) = %q, want /src/a", got)
	}
	if got := paneRepo(snap, "s2", key); got != "" {
		t.Errorf("pane の無いセッションは空: %q", got)
	}
	if got := paneRepo(nil, "s1", key); got != "" {
		t.Errorf("status.json が無ければ空: %q", got)
	}
}
