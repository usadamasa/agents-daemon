package tasklog

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// 同じリポジトリの main worktree、その下のディレクトリ、linked worktree はすべて main worktree のパスになる。
func TestRepoRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	git(t, root, "init", "-q", repo)
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt")
	git(t, repo, "worktree", "add", "-q", wt)

	for _, cwd := range []string{repo, filepath.Join(repo, ".git"), wt} {
		if got := RepoRoot(cwd); got != repo {
			t.Errorf("RepoRoot(%q) = %q, want %q", cwd, got, repo)
		}
	}
	for _, cwd := range []string{"", "relative", root} {
		if got := RepoRoot(cwd); got != "" {
			t.Errorf("RepoRoot(%q) = %q, want 空", cwd, got)
		}
	}
}
