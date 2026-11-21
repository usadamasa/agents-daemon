package refpoll

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// newWorkSessions は一時ディレクトリの作業の記録と、places を返す WorkSessions を組む。
func newWorkSessions(t *testing.T, dir string, places []Session) WorkSessions {
	t.Helper()
	log := applog.NewLogger(filepath.Join(dir, "daemon.log"), time.Now)
	t.Cleanup(func() { _ = log.Close() })
	return WorkSessions{Works: worklog.New(dir), Places: func() []Session { return places }, Log: log}
}

// PR を出したセッションを引く cwd は、作業の記録の cwd (hook が渡す今の cwd。セッションの途中で入った worktree も
// 映る) を pane の cwd (起動したディレクトリ) より先に使う。記録が無ければ pane の cwd。
func TestWorkSessionsLive_作業の記録のcwdを先に使う(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	w := newWorkSessions(t, t.TempDir(), []Session{{ID: "A", CWD: "/repo"}, {ID: "B", CWD: "/other"}})
	in := `{"session_id":"A","cwd":"/repo/.claude/worktrees/x","hook_event_name":"UserPromptSubmit","prompt":"直して"}`
	if _, err := w.Works.Ingest([]byte(in), now); err != nil {
		t.Fatal(err)
	}

	want := []Session{{ID: "A", CWD: "/repo/.claude/worktrees/x", Repo: "/repo/.claude/worktrees/x"}, {ID: "B", CWD: "/other"}}
	if got := w.Live(); !reflect.DeepEqual(got, want) {
		t.Errorf("Live() = %v, want %v", got, want)
	}
}

// PR を出したセッションは、作業の記録の prs から引く。終了したセッションの記録も含め、cwd は記録の cwd (#575)。
// 同じ PR を 2 つのセッションが記録していれば両方を返す (どちらへ導くかは Poller が決める)。
func TestWorkSessionsIssuers_作業の記録のPRから出したセッションを引く(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	w := newWorkSessions(t, t.TempDir(), nil)
	inputs := []string{
		`{"session_id":"A","cwd":"/a","hook_event_name":"UserPromptSubmit","prompt":"p"}`,
		`{"session_id":"A","cwd":"/a","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":{"stdout":"https://github.com/o/r/pull/1\n"}}`,
		`{"session_id":"A","cwd":"/a","hook_event_name":"SessionEnd","reason":"other"}`,
		`{"session_id":"B","cwd":"/b","hook_event_name":"UserPromptSubmit","prompt":"p"}`,
		`{"session_id":"B","cwd":"/b","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":{"stdout":"https://github.com/o/r/pull/2\n"}}`,
		`{"session_id":"C","cwd":"/c","hook_event_name":"UserPromptSubmit","prompt":"p"}`,
		`{"session_id":"C","cwd":"/c","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":{"stdout":"https://github.com/o/r/pull/2\n"}}`,
	}
	for _, in := range inputs {
		if _, err := w.Works.Ingest([]byte(in), now); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string][]Session{
		"pr:o/r#1": {{ID: "A", CWD: "/a", Repo: "/a"}},
		"pr:o/r#2": {{ID: "B", CWD: "/b", Repo: "/b"}, {ID: "C", CWD: "/c", Repo: "/c"}},
	}
	if got := w.Issuers(); !reflect.DeepEqual(got, want) {
		t.Errorf("Issuers() = %v, want %v", got, want)
	}
}

// worktree で PR を出して終了し、worktree が消えたセッションからも、本来のリポジトリの鍵で辺を導く (#606)。
func TestWorkSessionsIssuers_worktreeが消えても本来のrepoの鍵で導く(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	w := newWorkSessions(t, dir, nil)

	main := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(main, ".claude", "worktrees", "x")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", main},
		{"-C", main, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", main, "worktree", "add", "-q", "-b", "x", worktree},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wantRepo := gitrepo.RepoKey(main)

	inputs := []string{
		`{"session_id":"A","cwd":` + strconv.Quote(worktree) + `,"hook_event_name":"UserPromptSubmit","prompt":"p"}`,
		`{"session_id":"A","cwd":` + strconv.Quote(worktree) + `,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":{"stdout":"https://github.com/o/r/pull/1\n"}}`,
	}
	for _, in := range inputs {
		if _, err := w.Works.Ingest([]byte(in), now); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", main, "worktree", "remove", "--force", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}
	end := `{"session_id":"A","cwd":` + strconv.Quote(worktree) + `,"hook_event_name":"SessionEnd","reason":"other"}`
	if _, err := w.Works.Ingest([]byte(end), now); err != nil {
		t.Fatal(err)
	}

	issued := w.Issuers()["pr:o/r#1"]
	if len(issued) != 1 || issued[0].Repo != wantRepo {
		t.Fatalf("Issuers() = %+v, want repo %q", issued, wantRepo)
	}
	edges := edge.New(dir)
	if _, err := edges.Add(edge.Endpoint{Ref: "pr:o/r#1", CWD: main}, edge.Endpoint{Ref: "task:H/1", CWD: main}, "", now); err != nil {
		t.Fatal(err)
	}
	e, ok, err := edges.Anchor(issued[0].ID, issued[0].CWD, issued[0].Repo, "pr:o/r#1", now)
	if err != nil || !ok {
		t.Fatalf("Anchor() = %v, %v", ok, err)
	}
	if e.Before.Repo != wantRepo {
		t.Errorf("before の repo = %q, want %q", e.Before.Repo, wantRepo)
	}
}
