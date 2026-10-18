package board

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// locator は cwd ごとの Place を 1 回だけ求めて覚える。
type locator struct {
	locate func(cwd string) Place
	cache  map[string]Place
}

func (l locator) place(cwd string) Place {
	if p, ok := l.cache[cwd]; ok {
		return p
	}
	var p Place
	if l.locate != nil && cwd != "" {
		p = l.locate(cwd)
	}
	l.cache[cwd] = p
	return p
}

// row は cwd の行の鍵と見出し。git の外なら cwd そのものを鍵にする。
func (l locator) row(cwd string) (key, title string) {
	key = l.place(cwd).Repo
	if key == "" {
		key = cwd
	}
	if key == "" {
		return "", "(cwd 不明)"
	}
	return key, filepath.Base(key)
}

// sessionNames はセッションごとの短い名前を決める。branch を使い、同じ名前のセッションが複数あるか
// branch が無ければ session ID の先頭を足して見分ける。
// cwds は session ID → そのセッションの今の cwd。
func sessionNames(cwds map[string]string, l locator) map[string]string {
	base := map[string]string{}
	count := map[string]int{}
	ids := make([]string, 0, len(cwds))
	for id, cwd := range cwds {
		ids = append(ids, id)
		base[id] = l.place(cwd).Branch
		count[base[id]]++
	}
	sort.Strings(ids)
	names := map[string]string{}
	for _, id := range ids {
		switch {
		case base[id] == "":
			names[id] = shortID(id)
		case count[base[id]] > 1:
			names[id] = base[id] + " " + shortID(id)
		default:
			names[id] = base[id]
		}
	}
	return names
}

// shortID は session ID の先頭 8 文字 (UUID の最初の区切りまで)。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Locate は git で cwd の repo (main worktree のパス) と branch を求める。git の外や git が無ければゼロ値。
//
// `--git-common-dir` は linked worktree からでも main worktree の .git を指すので、同じリポジトリの
// worktree はすべて同じ行になる。bare リポジトリなど .git で終わらないときは common dir をそのまま使う。
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
