package deps

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// worktree の消えた終了セッションは、git で cwd を引けなくても作業の記録の repo の鍵 (worklog の repo) の列に置く
// (models/prlink の PlacedRepo)。同じ cwd のタスクのノードも同じ列に入り、移したメモと同じ repo にまとまる。
// 同じ cwd に鍵が複数あれば、cwd と違う鍵 (git で引けたときの鍵) を選ぶ。
func TestBuild_worktreeが消えた終了セッションを記録のrepoの鍵の列へ置く(t *testing.T) {
	const main = "/src/home/agents-daemon"
	const gone, goneTask = main + "/.claude/worktrees/x", main + "/.claude/worktrees/y"
	ended := t0.Add(time.Hour)
	exited := func(sid, cwd, repo string) worklog.Record {
		w := work(sid, cwd, "終わった作業 "+sid, t0)
		w.Repo, w.EndedAt = repo, &ended
		return w
	}
	d := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "L", CWD: main, AgentStatus: "working"}),
		Tasks:    []record.Task{task("Y", "1", "worktree のタスク", "pending", goneTask)},
		Works: []worklog.Record{
			work("L", main, "生きている作業", t0),
			exited("Z", gone, gone),
			exited("X", gone, main),
			exited("Y", goneTask, main),
		},
		Parking: []parking.Item{{ID: "m1", Repo: main, Status: parking.StatusOpen, Origin: parking.OriginAdopted, Kind: parking.KindScope, Title: "移した作業"}},
		Locate:  places(map[string]gitrepo.Place{main: {Repo: main, Branch: "main"}}),
	})

	if len(d.Repos) != 1 || d.Repos[0].Repo != main || len(d.Repos[0].Memos) != 1 {
		t.Errorf("repos = %+v", d.Repos)
	}
	for _, n := range d.Graph.Nodes {
		if n.Repo != main {
			t.Errorf("%s の repo = %q, want %q", n.Key, n.Repo, main)
		}
	}
	if got := nodes(d.Graph); got["sid:X"].Key == "" || got["task:Y/1"].Key == "" {
		t.Errorf("nodes = %v", keys(d.Graph.Nodes))
	}
}

// worktree でタスクを作り、main へ戻って終了したセッションのタスクは、作業の記録の最後の cwd がタスクの cwd と違う。
// worktree が消えても、タスクと TodoWrite のノードはタスクの記録の鍵で本来の repo の列へ置く (#618)。
func TestBuild_worktreeが消えた終了セッションのタスクをタスクの記録のrepoの鍵の列へ置く(t *testing.T) {
	const main = "/src/home/agents-daemon"
	const gone = main + "/.claude/worktrees/y"
	ended := t0.Add(time.Hour)
	w := work("Y", main, "終わった作業", t0)
	w.Repo, w.EndedAt = main, &ended
	tk := task("Y", "1", "worktree のタスク", "pending", gone)
	tk.Repo = main
	todos := record.TodoList{SessionID: "T", CWD: gone, Repo: main, Todos: []record.Todo{{Content: "worktree の TODO", Status: "pending"}}, UpdatedAt: t0}
	tw := work("T", main, "TODO の作業", t0)
	tw.Repo, tw.EndedAt = main, &ended
	d := build(input{
		PID:      42,
		Snapshot: snapshot(),
		Tasks:    []record.Task{tk},
		Todos:    []record.TodoList{todos},
		Works:    []worklog.Record{w, tw},
		Locate:   places(map[string]gitrepo.Place{main: {Repo: main, Branch: "main"}}),
	})

	if len(d.Repos) != 1 || d.Repos[0].Repo != main {
		t.Errorf("repos = %+v", d.Repos)
	}
	for _, n := range d.Graph.Nodes {
		if n.Repo != main {
			t.Errorf("%s の repo = %q, want %q", n.Key, n.Repo, main)
		}
	}
	if got := nodes(d.Graph); got["task:Y/1"].Key == "" {
		t.Errorf("nodes = %v", keys(d.Graph.Nodes))
	}
}

// 依存図の repo の鍵はパーキングのメモの repo (gitrepo.RepoKey) と同じになる。git の中、linked worktree、
// git の外のどれでも一致しないと、パーキングのメモがどの repo にも載らなくなる。
func TestLocatorRow_matchesRepoKey(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo := filepath.Join(root, "repo")
	git(root, "init", "-q", "-b", "main", repo)
	git(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt")
	git(repo, "worktree", "add", "-q", "-b", "topic", wt)

	l := locator{locate: gitrepo.Locate, cache: map[string]gitrepo.Place{}}
	for _, cwd := range []string{repo, wt, root} {
		key, _ := l.row(cwd)
		if want := gitrepo.RepoKey(cwd); key != want {
			t.Errorf("row(%q) の鍵 = %q, RepoKey = %q", cwd, key, want)
		}
	}
}
