package deps

import (
	"sort"
	"time"

	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// columnTier は依存図の右の列 (依存の無いノード) の段。小さいほど上に出す (#420)。
// 不明は手当てを要すると決められないので返事待ちに、外部の参照 (open と引けたものを含む) は手元では動かないが
// まだ動きうるものとして未着手の下に並べる。終了したセッションに残ったものは手を入れる余地が無いので、
// 「完了も表示」で利用者が求めて出す完了よりも下に置く (#446)。
var columnTier = map[string]int{
	graph.StatePermission: 0,
	graph.StateQuestion:   0,
	graph.StateError:      0,
	graph.StateLimit:      0,
	graph.StateWorking:    1,
	graph.StateDone:       2,
	graph.StateUnknown:    2,
	graph.StatePending:    3,
	graph.StateExternal:   4,
	graph.StateWaiting:    4,
	graph.StateCompleted:  5,
	graph.StateExited:     6,
}

// lastActive はノードの key → 最後に動いた時刻 (タスクと TodoWrite の記録の更新時刻、作業の記録の更新時刻)。作業の
// 記録の無いセッションは活動の変わった時刻を daemon が持たないので載らない。
func lastActive(tasks []record.Task, todos []record.TodoList, works []worklog.Record) map[string]time.Time {
	out := map[string]time.Time{}
	for _, t := range tasks {
		out[ref.Task(t.SessionID, t.ID)] = t.UpdatedAt
	}
	for _, list := range todos {
		for i := range list.Todos {
			out[ref.Todo(list.SessionID, i)] = list.UpdatedAt
		}
	}
	for _, w := range works {
		out[ref.SessionID(w.SessionID)] = w.UpdatedAt
	}
	return out
}

// columnOrder は全ノードの key を右の列に並べる順にする。どのノードが依存を持たないかは完了の表示で変わるので、
// 選ぶのはアプリに任せ、ここは順だけを決める。同じ段は最後に動いた時刻の新しい順 (時刻の無いものは後ろ)、
// それも同じなら repo、セッション名、key の順。
func columnOrder(nodes []protocol.GraphNode, active map[string]time.Time, repo func(cwd string) string) []string {
	type entry struct {
		key, repo, session string
		tier               int
		at                 time.Time
	}
	entries := make([]entry, 0, len(nodes))
	for _, n := range nodes {
		tier, ok := columnTier[n.State]
		if !ok {
			tier = columnTier[graph.StateUnknown]
		}
		entries = append(entries, entry{key: n.Key, repo: repo(n.CWD), session: n.Session, tier: tier, at: active[n.Key]})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch {
		case a.tier != b.tier:
			return a.tier < b.tier
		case !a.at.Equal(b.at):
			return a.at.After(b.at)
		case a.repo != b.repo:
			return a.repo < b.repo
		case a.session != b.session:
			return a.session < b.session
		}
		return graph.KeyLess(a.key, b.key)
	})
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.key
	}
	return out
}
