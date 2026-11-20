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

// columnTier は依存の無いノードを並べる段 (図の領域のセッションの枠と右の列)。小さいほど先に出す (#420、#467)。
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

// unplaced は置き場の分からない生きているノードを集める枠の鍵。rex の session の ID と重ならない。生きているノードは
// rex の block の pane からしか作らないので、置き場が無いのは rex の外だからではなく、daemon が置き場を書けていない
// (古い daemon、session.view を読めない) から (#662)。
const unplaced = "\x00rex-unplaced"

// groupByRex は order (columnOrder) を、アプリが描く枠 (#633) ごとにまとまる順へ並べ直す。枠は rex の session と、
// 置き場の分からない生きているノードの 2 種類で、中で一番先に来るノードの位置に並べる。枠の中は区切り (rex の window、
// 置き場の分からないものは Claude のセッション) ごとに、最初に来るノードの順にまとめる。どの枠にも入らないノード
// (終了したもの、参照) は自分だけの枠として、互いの順を保つ。アプリは辺の無いノードをこの順に枠へ入れる。
//
// 枠の位置は辺のあるノードも数えて決める。辺のあるノードは下の枠に出ないが、それを持つ枠は、同じ枠の辺の無いノード
// ごとアプリが線のある図へ引き入れる (frameTree) ので、下に並ぶ枠には辺のあるノードを持つ枠が無い。下の枠の並びは
// 枠に出るカードだけで決まり、辺のあるノードを除いて数える必要は無い。どのノードが辺を持つかは完了の表示で変わるので、
// ここでは決められない (#663)。
func groupByRex(order []string, nodes []protocol.GraphNode) []string {
	byKey := make(map[string]*protocol.GraphNode, len(nodes))
	for i := range nodes {
		byKey[nodes[i].Key] = &nodes[i]
	}
	frameAt := map[string]int{}
	partAt := map[[2]string]int{}
	type entry struct {
		key         string
		frame, part int
		at          int
	}
	entries := make([]entry, len(order))
	for i, key := range order {
		frame, part := key, ""
		if n := byKey[key]; n != nil {
			frame, part = frameOf(n)
		}
		if _, ok := frameAt[frame]; !ok {
			frameAt[frame] = i
		}
		if _, ok := partAt[[2]string{frame, part}]; !ok {
			partAt[[2]string{frame, part}] = i
		}
		entries[i] = entry{key: key, frame: frameAt[frame], part: partAt[[2]string{frame, part}], at: i}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.frame != b.frame {
			return a.frame < b.frame
		}
		return a.part < b.part
	})
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.key
	}
	return out
}

// frameOf はノードの枠と区切りの鍵。終わったノード (アプリが右の Ended の列に並べる) はノード自身。rex の置き場が
// あれば rex の session と window、置き場の分からない生きているノードは unplaced と Claude のセッション、どちらでも
// なければノード自身。
func frameOf(n *protocol.GraphNode) (frame, part string) {
	switch {
	case n.State == graph.StateCompleted || n.State == graph.StateExited:
		return n.Key, ""
	case n.Rex != nil:
		return n.Rex.SessionID, n.Rex.WindowID
	case n.Live && n.Kind != graph.KindRef:
		return unplaced, n.SessionID
	default:
		return n.Key, ""
	}
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

// columnOrder は全ノードの key を依存の無いノードの並ぶ順にする。どのノードが依存を持たないかは完了の表示で変わるので、
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
