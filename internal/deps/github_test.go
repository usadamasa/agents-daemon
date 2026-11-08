package deps

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// repo の owner/repo は repo の鍵ごとに 1 回だけ引く。ノードは自分の repo の鍵を持ち、worktree のノードも main worktree の鍵になる。
func TestBuild_githubRepo(t *testing.T) {
	const main, wt, other = "/src/home/agents-daemon", "/src/home/agents-daemon/.claude/worktrees/x", "/src/home/dotfile"
	calls := map[string]int{}
	d := build(input{
		Tasks: []record.Task{task("A", "1", "調査", "pending", main), task("B", "1", "実装", "pending", wt)},
		Parking: []parking.Item{{
			ID: "20261018T100000-bbbb", Repo: other, Status: parking.StatusOpen, Kind: parking.KindAside, Title: "メモだけの repo",
			Sources: []parking.Source{{SessionID: "C", CWD: other, At: t0}},
		}},
		Locate: places(map[string]gitrepo.Place{main: {Repo: main, Branch: "main"}, wt: {Repo: main, Branch: "x"}}),
		GitHubRepo: func(repo string) string {
			calls[repo]++
			if repo == main {
				return "o/r"
			}
			return ""
		},
	})

	got := repos(d)
	if got["agents-daemon"].GithubRepo != "o/r" {
		t.Errorf("agents-daemon = %q", got["agents-daemon"].GithubRepo)
	}
	if got["dotfile"].GithubRepo != "" {
		t.Errorf("GitHub でない repo = %q", got["dotfile"].GithubRepo)
	}
	if calls[main] != 1 || calls[other] != 1 {
		t.Errorf("calls = %v", calls)
	}
	if len(d.Graph.Nodes) != 2 {
		t.Fatalf("nodes = %+v", d.Graph.Nodes)
	}
	for _, n := range d.Graph.Nodes {
		if n.Repo != main {
			t.Errorf("%s repo = %q, want %q", n.Key, n.Repo, main)
		}
	}
}
