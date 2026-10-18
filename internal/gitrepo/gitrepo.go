// Package gitrepo は cwd がどのリポジトリのどの branch かを git で求める。タスクのボードの行の鍵と、
// パーキングのメモの repo が同じ規則になるよう、両方がここを使う。
package gitrepo

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// Place は cwd がどの repo のどの branch か。
type Place struct {
	// Repo は main worktree のパス。git の外なら空。
	Repo   string
	Branch string
}

// Locate は git で cwd の repo (main worktree のパス) と branch を求める。git の外や git が無ければゼロ値。
//
// `--git-common-dir` は linked worktree からでも main worktree の .git を指すので、同じリポジトリの
// worktree はすべて同じ repo になる。bare リポジトリなど .git で終わらないときは common dir をそのまま使う。
func Locate(cwd string) Place {
	if !filepath.IsAbs(cwd) {
		return Place{}
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir", "--abbrev-ref", "HEAD").Output() // #nosec G204 -- shell を通さず、cwd は引数 1 つとして渡る
	if err != nil {
		return Place{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return Place{}
	}
	common := filepath.Clean(lines[0])
	if filepath.Base(common) == ".git" {
		common = filepath.Dir(common)
	}
	branch := lines[1]
	if branch == "HEAD" {
		// detached HEAD は branch の名前にならない。
		branch = ""
	}
	return Place{Repo: common, Branch: branch}
}

// Key は p を求めた cwd の repo の鍵。git の外なら cwd そのものにする。ボードの行とパーキングのメモが
// 同じ行に並ぶよう、鍵の規則はここだけに置く。
func (p Place) Key(cwd string) string {
	if p.Repo != "" {
		return p.Repo
	}
	return cwd
}

// RepoKey は cwd の repo の鍵 (Place.Key)。
func RepoKey(cwd string) string {
	return Locate(cwd).Key(cwd)
}
