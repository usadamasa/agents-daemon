package parkcli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
)

// parkTestEnv は HOME と XDG_* を root へ向ける。
func parkTestEnv(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
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

func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTaskRecord(t *testing.T, root, sessionID, body string) {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "agents-daemon", "tasks", sessionID, task.ID+".json")
	writeTestConfig(t, path, body)
}

// park の help はパーキングのものを「メモ」と呼ぶ (#141)。
func TestParkHelpCallsItMemo(t *testing.T) {
	park := newParkCmd()
	for _, c := range append([]*cobra.Command{park}, park.Commands()...) {
		if strings.Contains(c.Short, "件") {
			t.Errorf("%s の Short が「件」と呼ぶ: %q", c.Name(), c.Short)
		}
		if usages := c.LocalFlags().FlagUsages() + c.PersistentFlags().FlagUsages(); strings.Contains(usages, "件") {
			t.Errorf("%s の flag が「件」と呼ぶ:\n%s", c.Name(), usages)
		}
	}
}

const parkInput = `{"kind": "aside", "title": "保持期間の根拠が無い", "detail": "d", "files": ["a.go"], "default_taken": "やらない", "session_id": "s1"}`

// repo は main worktree のパス (linked worktree からでも同じ)、task_id は
// そのセッションの進行中のタスクのうち最後に更新されたものになる。
func TestParkAdd(t *testing.T) {
	root := parkTestEnv(t)
	repo, wt := parkTestRepo(t, filepath.Join(root, "src", "home", "app"))
	writeTaskRecord(t, root, "s1", `{"id": "1", "subject": "a", "status": "in_progress", "session_id": "s1", "updated_at": "2026-10-19T10:00:00Z"}`)
	writeTaskRecord(t, root, "s1", `{"id": "2", "subject": "b", "status": "in_progress", "session_id": "s1", "updated_at": "2026-10-19T11:00:00Z"}`)
	writeTaskRecord(t, root, "s1", `{"id": "3", "subject": "c", "status": "pending", "session_id": "s1", "updated_at": "2026-10-19T12:00:00Z"}`)

	stdout, _, err := runPark(t, parkInput, "add", "--cwd", wt)
	if err != nil {
		t.Fatalf("park add: %v", err)
	}
	added := decodeAdded(t, stdout)
	if !added.Created {
		t.Errorf("新しいメモなのに created = false")
	}
	item := added.Item
	if item.Repo != gitrepo.Locate(repo).Repo || item.Repo == "" {
		t.Errorf("repo = %q, want %q", item.Repo, gitrepo.Locate(repo).Repo)
	}
	if len(item.Sources) != 1 || item.Sources[0].TaskID != "2" || item.Sources[0].CWD != wt || item.Sources[0].SessionID != "s1" {
		t.Errorf("sources = %+v", item.Sources)
	}

	// main worktree から同じメモを置くと同じメモにまとまる。
	stdout, _, err = runPark(t, strings.Replace(parkInput, `"s1"`, `"s2"`, 1), "add", "--cwd", repo)
	if err != nil {
		t.Fatal(err)
	}
	if again := decodeAdded(t, stdout); again.Created || again.Item.ID != item.ID || len(again.Item.Sources) != 2 || again.Item.Sources[1].TaskID != "" {
		t.Errorf("まとまっていない: %+v", again)
	}
}

// git の外は cwd を repo にする (依存図の repo と同じ)。
func TestParkAdd_outsideGit(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "work", "plain")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runPark(t, parkInput, "add", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	if item := decodeAdded(t, stdout).Item; item.Repo != cwd {
		t.Errorf("item = %+v", item)
	}
}

func TestParkAdd_invalidInput(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home")
	for name, in := range map[string]string{
		"壊れた JSON":      `{"kind": `,
		"kind が範囲外":     `{"kind": "decision", "title": "t", "session_id": "s1"}`,
		"kind が task":   `{"kind": "task", "title": "t", "session_id": "s1"}`,
		"session_id 無し": `{"kind": "aside", "title": "t"}`,
	} {
		if _, _, err := runPark(t, in, "add", "--cwd", cwd); err == nil {
			t.Errorf("%s: park add が通った", name)
		}
	}
}

// list は --repo と --status で絞る。set は status を書き換える。
func TestParkListAndSet(t *testing.T) {
	root := parkTestEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	other := filepath.Join(root, "src", "home", "b")
	for _, d := range []string{home, other} {
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
		t.Errorf("件数 = %d, want 2", len(got))
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
	if got := list("--repo", filepath.Join(root, "none")); got == nil || len(got) != 0 {
		t.Errorf("該当なしは空の配列: %+v", got)
	}
	if _, _, err := runPark(t, "", "list", "--cwd", home, "--status", "done"); err == nil {
		t.Errorf("範囲外の --status が通った")
	}
	// --from は元の status が違えば書き換えない (mod が送る前に取る)。
	if _, _, err := runPark(t, "", "set", first.ID, "--status", "handed", "--from", "open", "--cwd", home); err == nil {
		t.Errorf("discarded のメモを --from open で取れた")
	}
	out, _, err = runPark(t, "", "set", first.ID, "--status", "open", "--from", "discarded", "--cwd", home)
	if err != nil || decodeItem(t, out).Status != "open" {
		t.Fatalf("park set --from discarded: %v\n%s", err, out)
	}
}

// writeDaemonStatus は動いている daemon (このテストプロセス) の pid と、たった今書いた status.json (pane は panes) を置く。
func writeDaemonStatus(t *testing.T, root string, panes string) {
	t.Helper()
	state := filepath.Join(root, "state", "agents-daemon")
	writeTestConfig(t, filepath.Join(state, "daemon.pid"), strconv.Itoa(os.Getpid())+"\n")
	writeTestConfig(t, filepath.Join(state, "status.json"), `{"pid": `+strconv.Itoa(os.Getpid())+`, "updated_at": "`+time.Now().Add(time.Minute).Format(time.RFC3339)+`", "panes": [`+panes+`]}`)
}

// adopt はタスクを kind task のメモにし、タスクの記録を消す。
// セッションが終了したことは daemon の status.json で確かめ、daemon が動いていなければ移さない。
func TestParkAdopt(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	writeTaskRecord(t, root, "dead", `{"id": "4", "subject": "README を直す", "status": "pending", "session_id": "dead", "cwd": "`+cwd+`", "updated_at": "2026-10-18T09:00:00Z"}`)

	if _, stderr, err := runPark(t, "", "adopt", "dead", "4", "--session", "viewer", "--cwd", cwd); err == nil || !strings.Contains(stderr, "daemon が動いていない") {
		t.Fatalf("daemon が動いていないのに移した: %v %s", err, stderr)
	}
	writeDaemonStatus(t, root, `{"pane_id": "b1", "session_id": "dead", "cwd": "`+cwd+`"}`)
	if _, stderr, err := runPark(t, "", "adopt", "dead", "4", "--session", "viewer", "--cwd", cwd); err == nil || !strings.Contains(stderr, "生きている") {
		t.Fatalf("pane のあるセッションのタスクを移した: %v %s", err, stderr)
	}
	writeDaemonStatus(t, root, ``)
	out, _, err := runPark(t, "", "adopt", "dead", "4", "--session", "viewer", "--cwd", cwd)
	if err != nil {
		t.Fatalf("park adopt: %v", err)
	}
	if item := decodeAdded(t, out).Item; item.Kind != parking.KindTask || item.Title != "README を直す" {
		t.Errorf("item = %+v", item)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "tasks", "dead", "4.json")); !os.IsNotExist(err) {
		t.Errorf("タスクの記録が残った: %v", err)
	}
}

// --task はそのセッションがそのタスクの途中で置いたメモで絞る。--session が無ければ受け付けない。
func TestParkList_task(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	writeTaskRecord(t, root, "s1", `{"id": "3", "subject": "a", "status": "in_progress", "session_id": "s1"}`)
	out, _, err := runPark(t, parkInput, "add", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	during := decodeAdded(t, out).Item
	if _, _, err := runPark(t, strings.Replace(strings.Replace(parkInput, "保持期間", "別の件", 1), `"s1"`, `"s2"`, 1), "add", "--cwd", cwd); err != nil {
		t.Fatal(err)
	}

	out, _, err = runPark(t, "", "list", "--status", "open", "--task", "3", "--session", "s1", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	var items []parking.Item
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("list の JSON を読めない: %v\n%s", err, out)
	}
	if len(items) != 1 || items[0].ID != during.ID {
		t.Errorf("--task = %+v, want %s だけ", items, during.ID)
	}
	if _, _, err := runPark(t, "", "list", "--task", "3", "--cwd", cwd); err == nil {
		t.Errorf("--session の無い --task を受け付けた")
	}
}

// --here は --cwd の repo (linked worktree からでも main worktree の鍵) で絞る。--repo とは併用できない。
func TestParkList_here(t *testing.T) {
	root := parkTestEnv(t)
	repo, wt := parkTestRepo(t, filepath.Join(root, "src", "home", "app"))
	other := filepath.Join(root, "src", "home", "other")
	if err := os.MkdirAll(other, 0o750); err != nil {
		t.Fatal(err)
	}
	out, _, err := runPark(t, parkInput, "add", "--cwd", wt)
	if err != nil {
		t.Fatal(err)
	}
	mine := decodeAdded(t, out).Item
	if _, _, err := runPark(t, strings.Replace(parkInput, "保持期間", "別の件", 1), "add", "--cwd", other); err != nil {
		t.Fatal(err)
	}

	for _, cwd := range []string{repo, wt} {
		out, _, err := runPark(t, "", "list", "--status", "open", "--here", "--session", "s1", "--cwd", cwd)
		if err != nil {
			t.Fatalf("park list --here --cwd %s: %v", cwd, err)
		}
		var items []parking.Item
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("list の JSON を読めない: %v\n%s", err, out)
		}
		if len(items) != 1 || items[0].ID != mine.ID {
			t.Errorf("--here --cwd %s = %+v, want %s だけ", cwd, items, mine.ID)
		}
	}
	if _, _, err := runPark(t, "", "list", "--here", "--repo", repo, "--cwd", repo); err == nil {
		t.Errorf("--here と --repo を併用できた")
	}
}

// discard は open のメモのうち、最後に置かれてから --days 日より経ったものを
// discarded にし、捨てたメモを出す。--days は必須で 1 以上。
func TestParkDiscard(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	// add は今の時刻で置くので、古いメモは記録を直接置く。
	const old = "20260901T000000-0ld0ld00"
	writeTestConfig(t, filepath.Join(root, "state", "agents-daemon", "parking", old+".json"),
		`{"id":"`+old+`","repo":"`+cwd+`","kind":"aside","title":"古い","status":"open","sources":[{"session_id":"s1","cwd":"`+cwd+`","at":"2026-09-01T00:00:00Z"}]}`)
	out, _, err := runPark(t, parkInput, "add", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	fresh := decodeAdded(t, out).Item

	out, _, err = runPark(t, "", "discard", "--days", "30", "--session", "viewer", "--cwd", cwd)
	if err != nil {
		t.Fatalf("park discard: %v", err)
	}
	var items []parking.Item
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("discard の JSON を読めない: %v\n%s", err, out)
	}
	if len(items) != 1 || items[0].ID != old || items[0].Status != parking.StatusDiscarded {
		t.Errorf("discard = %+v, want %s だけ", items, old)
	}
	out, _, err = runPark(t, "", "list", "--status", "open", "--cwd", cwd)
	if err != nil {
		t.Fatal(err)
	}
	var open []parking.Item
	if err := json.Unmarshal([]byte(out), &open); err != nil {
		t.Fatalf("list の JSON を読めない: %v\n%s", err, out)
	}
	if len(open) != 1 || open[0].ID != fresh.ID {
		t.Errorf("open = %+v, want %s だけ", open, fresh.ID)
	}
	if _, _, err := runPark(t, "", "discard", "--cwd", cwd); err == nil {
		t.Errorf("--days の無い discard を受け付けた")
	}
	if _, _, err := runPark(t, "", "discard", "--days", "0", "--cwd", cwd); err == nil {
		t.Errorf("--days 0 を受け付けた")
	}
}
