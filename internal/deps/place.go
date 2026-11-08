package deps

import (
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
)

// locator は cwd ごとの Place と、repo の鍵ごとの owner/repo を 1 回だけ求めて覚える。
type locator struct {
	locate      func(cwd string) gitrepo.Place
	cache       map[string]gitrepo.Place
	gitHubRepo  func(repo string) string
	gitHubRepos map[string]string
}

// github は repo の鍵の owner/repo。GitHub の repo でなければ空。
func (l locator) github(repo string) string {
	if l.gitHubRepo == nil || repo == "" {
		return ""
	}
	r, ok := l.gitHubRepos[repo]
	if !ok {
		r = l.gitHubRepo(repo)
		l.gitHubRepos[repo] = r
	}
	return r
}

func (l locator) place(cwd string) gitrepo.Place {
	if p, ok := l.cache[cwd]; ok {
		return p
	}
	var p gitrepo.Place
	if l.locate != nil && cwd != "" {
		p = l.locate(cwd)
	}
	l.cache[cwd] = p
	return p
}

// row は cwd の repo の鍵と見出し。鍵はパーキングのメモの repo と同じ規則 (gitrepo.Place.Key) で決める。
func (l locator) row(cwd string) (key, title string) {
	key = l.place(cwd).Key(cwd)
	if key == "" {
		return "", "(cwd 不明)"
	}
	return key, filepath.Base(key)
}
