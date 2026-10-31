package deps

import (
	"reflect"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// 台帳のセッションを repo で束ね、最後に見た時刻の新しい順に並べる。同じ時刻なら鍵の順。repo の分からない
// セッション (鍵が空) は起こす先にできないので出さない。
func TestBuild_recentRepos(t *testing.T) {
	const home, dot, ws = "/src/home/agents-daemon", "/src/home/dotfile", "/work/space"
	ago := func(h int) time.Time { return t0.Add(-time.Duration(h) * time.Hour) }
	seen := func(id, repo string, h int) ledger.Session {
		return ledger.Session{SessionID: id, Repo: repo, LastSeen: ago(h)}
	}
	d := build(input{
		Locate: places(nil),
		Sessions: []ledger.Session{
			seen("A", home, 5),
			seen("B", dot, 30),
			seen("C", home, 1),
			seen("D", ws, 1),
			seen("E", "", 0),
		},
	})
	want := []protocol.RecentRepo{
		{Repo: home, Title: "agents-daemon", LastSeen: ago(1)},
		{Repo: ws, Title: "space", LastSeen: ago(1)},
		{Repo: dot, Title: "dotfile", LastSeen: ago(30)},
	}
	if !reflect.DeepEqual(d.RecentRepos, want) {
		t.Errorf("recent_repos = %+v, want %+v", d.RecentRepos, want)
	}
}

// 台帳が空でも recent_repos は空の配列 (null にしない。mod が配列として読む)。
func TestBuild_recentReposEmpty(t *testing.T) {
	d := build(input{Locate: places(nil)})
	if d.RecentRepos == nil || len(d.RecentRepos) != 0 {
		t.Errorf("recent_repos = %#v, want 空の配列", d.RecentRepos)
	}
}
