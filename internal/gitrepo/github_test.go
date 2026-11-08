package gitrepo

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOwnerRepo(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:o/r.git":             "o/r",
		"git@github.com:o/r":                 "o/r",
		"https://github.com/o/r.git":         "o/r",
		"https://github.com/o/r":             "o/r",
		"https://github.com/o/r/":            "o/r",
		"https://token@github.com/o/r.x.git": "o/r.x",
		"ssh://git@github.com/o/r.git":       "o/r",
		"https://gitlab.com/o/r.git":         "",
		"https://notgithub.com/o/r":          "",
		"git@github.com:o":                   "",
		"":                                   "",
	} {
		if got := ownerRepo(url); got != want {
			t.Errorf("ownerRepo(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestGitHubRepo(t *testing.T) {
	root, repo, _, wt := fixture(t)
	if got := GitHubRepo(repo); got != "" {
		t.Errorf("origin の無い repo = %q", got)
	}
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", "git@github.com:o/r.git").CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	for _, dir := range []string{repo, wt} {
		if got := GitHubRepo(dir); got != "o/r" {
			t.Errorf("GitHubRepo(%s) = %q", dir, got)
		}
	}
	if got := GitHubRepo(root); got != "" {
		t.Errorf("git の外 = %q", got)
	}
	if got := GitHubRepo(filepath.Join("relative", "path")); got != "" {
		t.Errorf("相対パス = %q", got)
	}
}
