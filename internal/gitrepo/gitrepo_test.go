package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixture は root の下に main worktree (repo)、その下のディレクトリ (sub)、linked worktree (wt) を作る。
// root 自体は git の外。
func fixture(t *testing.T) (root, repo, sub, wt string) {
	t.Helper()
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
	repo = filepath.Join(root, "repo")
	git(root, "init", "-q", "-b", "main", repo)
	git(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	wt = filepath.Join(root, "wt")
	git(repo, "worktree", "add", "-q", "-b", "topic", wt)
	sub = filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	return root, repo, sub, wt
}

// 同じリポジトリの main worktree、その下のディレクトリ、linked worktree は同じ repo になり、branch はそれぞれのもの。
func TestLocate(t *testing.T) {
	root, repo, sub, wt := fixture(t)
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

// git の中と linked worktree は main worktree のパス、git の外は cwd そのものが鍵になり、
// RepoKey と Place.Key は同じ鍵を返す。
func TestKey(t *testing.T) {
	root, repo, sub, wt := fixture(t)
	for cwd, want := range map[string]string{repo: repo, sub: repo, wt: repo, root: root, "": ""} {
		if got := Locate(cwd).Key(cwd); got != want {
			t.Errorf("Locate(%q).Key = %q, want %q", cwd, got, want)
		}
		if got := RepoKey(cwd); got != want {
			t.Errorf("RepoKey(%q) = %q, want %q", cwd, got, want)
		}
	}
}
