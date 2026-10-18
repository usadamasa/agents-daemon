package tasklog

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoRoot は cwd のリポジトリの main worktree のパスを返す。git の外や git が無ければ空。
//
// `--git-common-dir` は linked worktree からでも main worktree の .git を指すので、同じリポジトリの
// worktree はすべて同じ値になる。bare リポジトリなど .git で終わらないときは common dir をそのまま使う。
func RepoRoot(cwd string) string {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return ""
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir").Output() // #nosec G204 -- shell を通さず、cwd は引数 1 つとして渡る
	if err != nil {
		return ""
	}
	common := filepath.Clean(strings.TrimSpace(string(out)))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}
