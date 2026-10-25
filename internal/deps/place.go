package deps

import (
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
)

// locator は cwd ごとの Place を 1 回だけ求めて覚える。
type locator struct {
	locate func(cwd string) gitrepo.Place
	cache  map[string]gitrepo.Place
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
