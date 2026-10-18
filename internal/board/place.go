package board

import (
	"path/filepath"
	"sort"

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

// row は cwd の行の鍵と見出し。鍵はパーキングの件の repo と同じ規則 (gitrepo.Place.Key) で決める。
func (l locator) row(cwd string) (key, title string) {
	key = l.place(cwd).Key(cwd)
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
