package deps

import (
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// locator は cwd ごとの Place と、repo の鍵ごとの owner/repo を 1 回だけ求めて覚える。
type locator struct {
	locate func(cwd string) gitrepo.Place
	cache  map[string]gitrepo.Place
	// recorded は作業の記録の cwd → repo の鍵 (recordedRepos)。git で cwd を引けないときに使う。
	recorded    map[string]string
	gitHubRepo  func(repo string) string
	gitHubRepos map[string]string
}

// recordedRepos は作業の記録の cwd → repo の鍵。worktree の消えたセッションの cwd を本来の repo へ戻す
// (models/prlink の PlacedRepo)。同じ cwd に鍵が複数あれば、cwd と違う鍵 (git で引けたときの鍵) を優先し、
// その中で辞書順の最小を選ぶ。記録の並びに依らず tick ごとに同じ列になる。
func recordedRepos(works []worklog.Record) map[string]string {
	out := map[string]string{}
	for _, w := range works {
		if w.CWD == "" || w.Repo == "" {
			continue
		}
		if cur, ok := out[w.CWD]; ok && !betterKey(w.CWD, w.Repo, cur) {
			continue
		}
		out[w.CWD] = w.Repo
	}
	return out
}

// betterKey は cwd の鍵として next が cur より優先されるか。
func betterKey(cwd, next, cur string) bool {
	if (next != cwd) != (cur != cwd) {
		return next != cwd
	}
	return next < cur
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

// row は cwd の repo の鍵と見出し。鍵はパーキングのメモの repo と同じ規則 (gitrepo.Place.Key) で決め、git で cwd を
// 引けなければ作業の記録の鍵を使う。
func (l locator) row(cwd string) (key, title string) {
	p := l.place(cwd)
	key = p.Key(cwd)
	if r, ok := l.recorded[cwd]; ok && p.Repo == "" {
		key = r
	}
	if key == "" {
		return "", "(cwd 不明)"
	}
	return key, filepath.Base(key)
}
