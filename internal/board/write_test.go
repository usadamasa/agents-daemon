package board

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/domain"
)

func paths(t *testing.T) apppath.Paths {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	p, err := apppath.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBoard(t *testing.T, p apppath.Paths, v Viewer) Board {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, p, v); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var b Board
	if err := json.Unmarshal(buf.Bytes(), &b); err != nil {
		t.Fatalf("JSON = %s: %v", buf.String(), err)
	}
	return b
}

// daemon が動いていなくても、開いたセッションの domain のタスクを描く。domain は config と cwd で決め、
// 開いたセッションの記録が業務の側にあれば業務を描く。
func TestWrite(t *testing.T) {
	p := paths(t)
	writeFile(t, p.ConfigFile(), `{"homePaths": ["/src/home"]}`)
	tasks := filepath.Join(p.StateDir(), "tasks")
	writeFile(t, filepath.Join(tasks, "home", "H", "1.json"), `{"id":"1","subject":"私用","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	writeFile(t, filepath.Join(tasks, "work", "W", "1.json"), `{"id":"1","subject":"業務","status":"pending","session_id":"W","cwd":"/work/b"}`)

	home := writeBoard(t, p, Viewer{CWD: "/src/home/a", UserHome: "/u"})
	if home.Domain != domain.Home || home.DaemonRunning || len(home.Rows) != 1 || home.Rows[0].Cards[0].Subject != "私用" {
		t.Errorf("home = %+v", home)
	}
	work := writeBoard(t, p, Viewer{CWD: "/work/b", UserHome: "/u"})
	if work.Domain != domain.Work || len(work.Rows) != 1 || work.Rows[0].Cards[0].Subject != "業務" {
		t.Errorf("work = %+v", work)
	}
	// 私用の cwd でも、記録が業務の側にあるセッションが開いたら業務を描く。
	moved := writeBoard(t, p, Viewer{SessionID: "W", CWD: "/src/home/a", UserHome: "/u"})
	if moved.Domain != domain.Work {
		t.Errorf("moved.Domain = %q, want work", moved.Domain)
	}
}

// 同じリポジトリの main worktree、その下のディレクトリ、linked worktree は同じ repo になり、branch はそれぞれのもの。
func TestLocate(t *testing.T) {
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
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	for cwd, want := range map[string]Place{repo: {repo, "main"}, sub: {repo, "main"}, wt: {repo, "topic"}} {
		if got := Locate(cwd); got != want {
			t.Errorf("Locate(%q) = %+v, want %+v", cwd, got, want)
		}
	}
	for _, cwd := range []string{"", "relative", root} {
		if got := Locate(cwd); got != (Place{}) {
			t.Errorf("Locate(%q) = %+v, want ゼロ値", cwd, got)
		}
	}
}
