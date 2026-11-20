package tasklog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// worktreeStore は repo とその下の worktree のディレクトリを作り、鍵を求めた cwd を数える Store を返す。鍵は
// ディレクトリがあれば repo、無ければ cwd そのもの (gitrepo.RepoKey が git で引けないときと同じ)。
func worktreeStore(t *testing.T) (Store, string, string, *[]string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(repo, ".claude", "worktrees", "x")
	if err := os.MkdirAll(worktree, 0o750); err != nil {
		t.Fatal(err)
	}
	s := New(t.TempDir())
	asked := &[]string{}
	s.repoKey = func(cwd string) string {
		*asked = append(*asked, cwd)
		if _, err := os.Stat(cwd); err == nil {
			return repo
		}
		return cwd
	}
	return s, repo, worktree, asked
}

// withCWD は fixture の hook 入力の cwd を差し替える。
func withCWD(t *testing.T, base []byte, cwd string) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		t.Fatal(err)
	}
	m["cwd"] = cwd
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustIngestAt(t *testing.T, s Store, input []byte, at time.Time) {
	t.Helper()
	if _, err := s.Ingest(input, at); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
}

// タスクの記録は cwd の repo の鍵を残す。鍵は記録が新しいか cwd が変わったときだけ求め、cwd が無くなっていれば
// 記録の鍵を残す (worklog の Store.setCWD と同じ規則、models/prlink の PRRepoKey)。worktree でタスクを作り、
// worktree が消えた後に更新しても、鍵は worktree のパスにならない (#618)。
func TestIngest_タスクの記録にrepoの鍵を残しworktreeが消えても上書きしない(t *testing.T) {
	s, repo, worktree, asked := worktreeStore(t)
	update := map[string]any{"taskId": "1", "status": "in_progress"}
	ok := map[string]any{"success": true, "taskId": "1", "updatedFields": []string{"status"}}

	mustIngestAt(t, s, withCWD(t, fixture(t, "task-create-alpha"), worktree), t0)
	mustIngestAt(t, s, withCWD(t, withTool(t, fixture(t, "task-update-completed"), "TaskUpdate", update, ok), worktree), t0.Add(time.Minute))
	removeDir(t, worktree)
	listed := withTool(t, fixture(t, "task-list"), "TaskList", nil,
		map[string]any{"tasks": []map[string]any{{"id": "1", "subject": "alpha", "status": "in_progress", "blockedBy": []string{}}}})
	mustIngestAt(t, s, withCWD(t, listed, worktree), t0.Add(2*time.Minute))

	got := byID(t, s)["1"]
	if got.CWD != worktree || got.Repo != repo {
		t.Errorf("記録 = cwd %q repo %q, want cwd %q repo %q", got.CWD, got.Repo, worktree, repo)
	}
	if want := []string{worktree}; !reflect.DeepEqual(*asked, want) {
		t.Errorf("repo の鍵を求めた cwd = %v, want %v", *asked, want)
	}
}

// cwd が変わったら鍵を求め直す (worktree から main へ戻ったセッションの更新)。
func TestIngest_cwdが変わったらタスクのrepoの鍵を求め直す(t *testing.T) {
	s, repo, worktree, asked := worktreeStore(t)
	update := map[string]any{"taskId": "1", "status": "in_progress"}
	ok := map[string]any{"success": true, "taskId": "1", "updatedFields": []string{"status"}}

	mustIngestAt(t, s, withCWD(t, fixture(t, "task-create-alpha"), worktree), t0)
	mustIngestAt(t, s, withCWD(t, withTool(t, fixture(t, "task-update-completed"), "TaskUpdate", update, ok), repo), t0.Add(time.Minute))

	got := byID(t, s)["1"]
	if got.CWD != repo || got.Repo != repo {
		t.Errorf("記録 = cwd %q repo %q, want cwd %q repo %q", got.CWD, got.Repo, repo, repo)
	}
	if want := []string{worktree, repo}; !reflect.DeepEqual(*asked, want) {
		t.Errorf("repo の鍵を求めた cwd = %v, want %v", *asked, want)
	}
}

// 無くなった cwd からは鍵を求め直さない。main で作ったタスクを、worktree へ入って消された後の hook が初めて
// worktree の cwd で更新しても、鍵は worktree のパスにならない (models/prlink の PRRepoKeyOnCwd が破る形)。
func TestIngest_無くなったcwdからはタスクのrepoの鍵を求め直さない(t *testing.T) {
	s, repo, worktree, asked := worktreeStore(t)
	update := map[string]any{"taskId": "1", "status": "in_progress"}
	ok := map[string]any{"success": true, "taskId": "1", "updatedFields": []string{"status"}}

	mustIngestAt(t, s, withCWD(t, fixture(t, "task-create-alpha"), repo), t0)
	removeDir(t, worktree)
	mustIngestAt(t, s, withCWD(t, withTool(t, fixture(t, "task-update-completed"), "TaskUpdate", update, ok), worktree), t0.Add(time.Minute))

	got := byID(t, s)["1"]
	if got.CWD != worktree || got.Repo != repo {
		t.Errorf("記録 = cwd %q repo %q, want cwd %q repo %q", got.CWD, got.Repo, worktree, repo)
	}
	if want := []string{repo}; !reflect.DeepEqual(*asked, want) {
		t.Errorf("repo の鍵を求めた cwd = %v, want %v", *asked, want)
	}
}

// TaskList で初めて知ったタスクが複数あっても、1 回の hook 入力では cwd の鍵を 1 回だけ求める。
func TestIngest_TaskListは鍵を1回だけ求める(t *testing.T) {
	s, repo, worktree, asked := worktreeStore(t)
	listed := withTool(t, fixture(t, "task-list"), "TaskList", nil,
		map[string]any{"tasks": []map[string]any{{"id": "1", "subject": "a", "status": "pending", "blockedBy": []string{}}, {"id": "2", "subject": "b", "status": "pending", "blockedBy": []string{}}}})
	mustIngestAt(t, s, withCWD(t, listed, worktree), t0)

	for id, task := range byID(t, s) {
		if task.Repo != repo {
			t.Errorf("タスク %s の repo = %q, want %q", id, task.Repo, repo)
		}
	}
	if want := []string{worktree}; !reflect.DeepEqual(*asked, want) {
		t.Errorf("repo の鍵を求めた cwd = %v, want %v", *asked, want)
	}
}

// TodoWrite のリストも同じ規則で鍵を残す。リスト全体を置き換えても、worktree が消えた後の書き込みは記録の鍵を残す。
func TestIngest_TodoWriteのrepoの鍵をworktreeが消えても上書きしない(t *testing.T) {
	s, repo, worktree, asked := worktreeStore(t)
	todos := []map[string]any{{"content": "a", "status": "pending", "activeForm": "doing a"}}
	input := withCWD(t, withTool(t, fixture(t, "task-list"), "TodoWrite", map[string]any{"todos": todos}, map[string]any{}), worktree)

	mustIngestAt(t, s, input, t0)
	removeDir(t, worktree)
	mustIngestAt(t, s, input, t0.Add(time.Minute))

	data, err := os.ReadFile(filepath.Join(s.Dir, fixtureSession, todosFile))
	if err != nil {
		t.Fatal(err)
	}
	var list record.TodoList
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if list.CWD != worktree || list.Repo != repo {
		t.Errorf("todos.json = cwd %q repo %q, want cwd %q repo %q", list.CWD, list.Repo, worktree, repo)
	}
	if want := []string{worktree}; !reflect.DeepEqual(*asked, want) {
		t.Errorf("repo の鍵を求めた cwd = %v, want %v", *asked, want)
	}
}

func removeDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}
