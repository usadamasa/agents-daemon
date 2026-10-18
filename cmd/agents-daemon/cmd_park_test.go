package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
)

// parkTestEnv は HOME と XDG_* を root へ向け、homePaths に ~/src/home を置く。
func parkTestEnv(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeTestConfig(t, filepath.Join(root, "config", "agents-daemon", "config.json"), `{"homePaths": ["~/src/home"]}`)
	return root
}

// parkTestRepo は dir に main と linked worktree (wt) を持つリポジトリを作る。
func parkTestRepo(t *testing.T, dir string) (repo, worktree string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "initial")
	worktree = filepath.Join(dir, ".claude", "worktrees", "wt")
	run("worktree", "add", "-q", worktree, "-b", "wt")
	return dir, worktree
}

func runPark(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newParkCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

func decodeItem(t *testing.T, s string) parking.Item {
	t.Helper()
	var item parking.Item
	if err := json.Unmarshal([]byte(s), &item); err != nil {
		t.Fatalf("item の JSON を読めない: %v\n%s", err, s)
	}
	return item
}

// decodeAdded は park add の stdout ({"created": bool, "item": {...}}) を読む。
func decodeAdded(t *testing.T, s string) parking.Added {
	t.Helper()
	var added parking.Added
	if err := json.Unmarshal([]byte(s), &added); err != nil || added.Item.ID == "" {
		t.Fatalf("add の JSON を読めない: %v\n%s", err, s)
	}
	return added
}

func writeTaskRecord(t *testing.T, root, d, sessionID, body string) {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "agents-daemon", "tasks", d, sessionID, task.ID+".json")
	writeTestConfig(t, path, body)
}

const parkInput = `{"kind": "aside", "title": "保持期間の根拠が無い", "detail": "d", "files": ["a.go"], "default_taken": "やらない", "session_id": "s1"}`

// domain は cwd の判定、repo は main worktree のパス (linked worktree からでも同じ)、task_id は
// そのセッションの進行中のタスクのうち最後に更新されたものになる。
func TestParkAdd(t *testing.T) {
	root := parkTestEnv(t)
	repo, wt := parkTestRepo(t, filepath.Join(root, "src", "home", "app"))
	writeTaskRecord(t, root, "home", "s1", `{"id": "1", "subject": "a", "status": "in_progress", "session_id": "s1", "updated_at": "2026-10-19T10:00:00Z"}`)
	writeTaskRecord(t, root, "home", "s1", `{"id": "2", "subject": "b", "status": "in_progress", "session_id": "s1", "updated_at": "2026-10-19T11:00:00Z"}`)
	writeTaskRecord(t, root, "home", "s1", `{"id": "3", "subject": "c", "status": "pending", "session_id": "s1", "updated_at": "2026-10-19T12:00:00Z"}`)

	stdout, _, err := runPark(t, parkInput, "add", "--cwd", wt)
	if err != nil {
		t.Fatalf("park add: %v", err)
	}
	added := decodeAdded(t, stdout)
	if !added.Created {
		t.Errorf("新しいメモなのに created = false")
	}
	item := added.Item
	if item.Domain != "home" || item.Repo != gitrepo.Locate(repo).Repo || item.Repo == "" {
		t.Errorf("domain / repo = %q / %q, want home / %q", item.Domain, item.Repo, gitrepo.Locate(repo).Repo)
	}
	if len(item.Sources) != 1 || item.Sources[0].TaskID != "2" || item.Sources[0].CWD != wt || item.Sources[0].SessionID != "s1" {
		t.Errorf("sources = %+v", item.Sources)
	}

	// main worktree から同じ件を置くと同じ件にまとまる。
	stdout, _, err = runPark(t, strings.Replace(parkInput, `"s1"`, `"s2"`, 1), "add", "--cwd", repo)
	if err != nil {
		t.Fatal(err)
	}
	if again := decodeAdded(t, stdout); again.Created || again.Item.ID != item.ID || len(again.Item.Sources) != 2 || again.Item.Sources[1].TaskID != "" {
		t.Errorf("まとまっていない: %+v", again)
	}
}

// 業務の側にタスクの記録があるセッションは、cwd が私用でも業務に置く (ボードと同じ規則)。
func TestParkAdd_sessionTiltedToWork(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	writeTaskRecord(t, root, "work", "s1", `{"id": "1", "subject": "a", "status": "in_progress", "session_id": "s1"}`)
	stdout, _, err := runPark(t, parkInput, "add", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	if item := decodeAdded(t, stdout).Item; item.Domain != "work" || item.Sources[0].TaskID != "1" {
		t.Errorf("item = %+v, want work に task 1 で置く", item)
	}
}

// git の外は cwd を repo にし (ボードの行と同じ)、homePaths の外は work に置く。
func TestParkAdd_outsideGitIsWork(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "work", "plain")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runPark(t, parkInput, "add", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	if item := decodeAdded(t, stdout).Item; item.Domain != "work" || item.Repo != cwd {
		t.Errorf("item = %+v", item)
	}
}

func TestParkAdd_invalidInput(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home")
	for name, in := range map[string]string{
		"壊れた JSON":      `{"kind": `,
		"kind が範囲外":     `{"kind": "decision", "title": "t", "session_id": "s1"}`,
		"session_id 無し": `{"kind": "aside", "title": "t"}`,
	} {
		if _, _, err := runPark(t, in, "add", "--cwd", cwd); err == nil {
			t.Errorf("%s: park add が通った", name)
		}
	}
}

// list は cwd の domain の件だけを出し、--repo と --status で絞る。set は status を書き換える。
func TestParkListAndSet(t *testing.T) {
	root := parkTestEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	other := filepath.Join(root, "src", "home", "b")
	work := filepath.Join(root, "work")
	for _, d := range []string{home, other, work} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	out, _, err := runPark(t, parkInput, "add", "--cwd", home)
	if err != nil {
		t.Fatal(err)
	}
	first := decodeAdded(t, out).Item
	if _, _, err := runPark(t, strings.Replace(parkInput, "保持期間", "別の件", 1), "add", "--cwd", other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runPark(t, parkInput, "add", "--cwd", work); err != nil {
		t.Fatal(err)
	}

	list := func(args ...string) []parking.Item {
		t.Helper()
		out, _, err := runPark(t, "", append([]string{"list"}, args...)...)
		if err != nil {
			t.Fatalf("park list %v: %v", args, err)
		}
		var items []parking.Item
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("list の JSON を読めない: %v\n%s", err, out)
		}
		return items
	}
	if got := list("--cwd", home); len(got) != 2 {
		t.Errorf("home の件数 = %d, want 2 (work の件を出さない)", len(got))
	}
	if got := list("--cwd", home, "--repo", home); len(got) != 1 || got[0].ID != first.ID {
		t.Errorf("--repo で絞れていない: %+v", got)
	}

	out, _, err = runPark(t, "", "set", first.ID, "--status", "discarded", "--cwd", home)
	if err != nil || decodeItem(t, out).Status != "discarded" {
		t.Fatalf("park set: %v\n%s", err, out)
	}
	if got := list("--cwd", home, "--status", "open"); len(got) != 1 || got[0].ID == first.ID {
		t.Errorf("--status で絞れていない: %+v", got)
	}
	if got := list("--cwd", work, "--repo", home); got == nil || len(got) != 0 {
		t.Errorf("該当なしは空の配列: %+v", got)
	}
	if _, _, err := runPark(t, "", "list", "--cwd", home, "--status", "done"); err == nil {
		t.Errorf("範囲外の --status が通った")
	}
	if _, _, err := runPark(t, "", "set", first.ID, "--status", "discarded", "--cwd", work); err == nil {
		t.Errorf("別の domain から書き換えられた")
	}
}

// --here は --cwd の repo (main worktree のパス) で絞る。linked worktree からでも main で置いたメモが出る。
func TestParkList_here(t *testing.T) {
	root := parkTestEnv(t)
	repo, wt := parkTestRepo(t, filepath.Join(root, "src", "home", "app"))
	other := filepath.Join(root, "src", "home", "other")
	if err := os.MkdirAll(other, 0o750); err != nil {
		t.Fatal(err)
	}
	out, _, err := runPark(t, parkInput, "add", "--cwd", repo)
	if err != nil {
		t.Fatal(err)
	}
	mine := decodeAdded(t, out).Item
	if _, _, err := runPark(t, parkInput, "add", "--cwd", other); err != nil {
		t.Fatal(err)
	}

	out, _, err = runPark(t, "", "list", "--here", "--status", "open", "--cwd", wt)
	if err != nil {
		t.Fatal(err)
	}
	var items []parking.Item
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("list の JSON を読めない: %v\n%s", err, out)
	}
	if len(items) != 1 || items[0].ID != mine.ID {
		t.Errorf("--here = %+v, want %s だけ", items, mine.ID)
	}
	if _, _, err := runPark(t, "", "list", "--here", "--repo", repo, "--cwd", wt); err == nil {
		t.Errorf("--here と --repo を同時に受け付けた")
	}
}
