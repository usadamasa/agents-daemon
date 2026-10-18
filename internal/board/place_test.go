package board

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
)

// ボードの行の鍵はパーキングの件の repo (gitrepo.RepoKey) と同じになる。git の中、linked worktree、
// git の外のどれでも一致しないと、パーキングの件がボードのどの行にも載らなくなる。
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
