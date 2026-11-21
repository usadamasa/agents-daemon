package deps

import (
	"reflect"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// タスクの題と辺の note の #番号 を owner/repo に解き、daemon が引いた参照と題を添える (#474)。repo を省いた形は
// タスクか辺の端点の cwd の repo で補い、repo 名は daemon が知っている owner/repo で引く。
func TestGraph_mentions(t *testing.T) {
	const ws, ad = "/w/workspace", "/w/agents-daemon"
	github := map[string]string{ws: "usadamasa/workspace", ad: "usadamasa/agents-daemon"}
	b := build(input{
		Tasks: []record.Task{
			task("A", "1", "#469 の PR をマージ判断へ回す", "pending", ws),
			task("A", "2", "agents-daemon #366 (根拠 1 セッション) を peer で処理", "pending", ws),
			task("A", "3", "番号の無い題", "pending", ws),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "session:ad-469", CWD: ad}, After: edge.Endpoint{Ref: "task:A/1", CWD: ws}, Note: "agents-daemon#469 の PR"},
			{ID: "e2", Before: edge.Endpoint{Ref: "session:ad-392", CWD: ad}, After: edge.Endpoint{Ref: "task:A/3", CWD: ws}, Note: "#392 の PR の報告待ち"},
		},
		Locate:     places(map[string]gitrepo.Place{ws: {Repo: ws}, ad: {Repo: ad}}),
		GitHubRepo: func(repo string) string { return github[repo] },
		RefStates:  map[string]string{"issue:usadamasa/agents-daemon#469": "closed", "pr:usadamasa/agents-daemon#366": "merged"},
		RefTitles:  map[string]string{"issue:usadamasa/agents-daemon#469": "依存図の題を読めるようにする", "pr:usadamasa/agents-daemon#366": "根拠 1 セッションの候補を low に"},
	})
	got := nodes(b.Graph)
	for key, want := range map[string][]protocol.RefMention{
		// 素の #N はタスクの cwd の repo で補う。引いていない参照は ref と題を持たない。
		"task:A/1": {{Repo: "usadamasa/workspace", Number: 469}},
		// 既知の repo 名の後の空白 1 つの #N は、その repo の番号。
		"task:A/2": {{Repo: "usadamasa/agents-daemon", Number: 366, Ref: "pr:usadamasa/agents-daemon#366", Title: "根拠 1 セッションの候補を low に"}},
		"task:A/3": nil,
	} {
		if !reflect.DeepEqual(got[key].Mentions, want) {
			t.Errorf("%s の mentions = %+v, want %+v", key, got[key].Mentions, want)
		}
	}
	links := map[string][]protocol.RefMention{}
	for _, l := range b.Graph.Links {
		if l.Edge != nil {
			links[l.Edge.ID] = l.Mentions
		}
	}
	want := map[string][]protocol.RefMention{
		"e1": {{Repo: "usadamasa/agents-daemon", Number: 469, Ref: "issue:usadamasa/agents-daemon#469", Title: "依存図の題を読めるようにする"}},
		// note の素の #N は、辺が触れる repo が 2 つなので補わない (models/noterepo)。
		"e2": nil,
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("辺の mentions = %+v, want %+v", links, want)
	}
}
