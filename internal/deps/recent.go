package deps

import (
	"path/filepath"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// recentRepos は台帳のセッションを repo で束ね、最後に見た時刻の新しい順 (同じ時刻なら鍵の順) に並べる。
// 鍵が空のセッションは起こす先にできないので除く。
func recentRepos(sessions []ledger.Session) []protocol.RecentRepo {
	latest := map[string]protocol.RecentRepo{}
	for _, s := range sessions {
		if s.Repo == "" {
			continue
		}
		if r, ok := latest[s.Repo]; ok && !s.LastSeen.After(r.LastSeen) {
			continue
		}
		latest[s.Repo] = protocol.RecentRepo{Repo: s.Repo, Title: filepath.Base(s.Repo), LastSeen: s.LastSeen}
	}
	out := make([]protocol.RecentRepo, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}
