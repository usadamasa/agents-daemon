// Package handed は渡したメモ (parking の handed) の宛先を依存図の中で解く (#292)。アプリは宛先の文字列を
// 切り出さず、ここが解いたノードの key と repo の鍵だけを見る。
package handed

import (
	"sort"
	"time"

	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
)

// Resolver は図に描くノードと作業から宛先を解く。
type Resolver struct {
	cards []graph.Card
	drawn map[string]bool
	names map[string]string
	row   func(cwd string) (key, title string)
}

// NewResolver は cards (ノードの材料) と nodes (図に描くノード) から Resolver を作る。names は session ID → 表示名、
// row は cwd → repo の鍵と title (依存図の repo と同じ規則)。
func NewResolver(cards []graph.Card, nodes []protocol.GraphNode, names map[string]string, row func(cwd string) (key, title string)) Resolver {
	drawn := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		drawn[n.Key] = true
	}
	return Resolver{cards: cards, drawn: drawn, names: names, row: row}
}

// To は宛先 (Item.HandedTo) を解く。sid: はノード (node) と表示名、repo: は repo の鍵と title。worktree へ起こした
// セッションも main worktree の repo に入る。
func (r Resolver) To(to string) protocol.HandedTo {
	out := protocol.HandedTo{Ref: to}
	switch sessionID, repo := parking.Destination(to); {
	case sessionID != "":
		out.Node = r.node(sessionID)
		out.Label = r.names[sessionID]
	case repo != "":
		out.Repo, out.Label = r.row(repo)
	}
	return out
}

// node はセッションへ渡したメモを出すノード。セッションのノードが図にあればそれ、無ければそのセッションのタスクと
// TodoWrite の項目のノードから、進行中、未着手、完了の順に、同じなら Key の順 (graph.KeyLess) で最初のもの。
// どれも図に無ければ空。
func (r Resolver) node(sessionID string) string {
	if key := ref.SessionID(sessionID); r.drawn[key] {
		return key
	}
	var best graph.Card
	found := false
	for _, c := range r.cards {
		if c.SessionID != sessionID || c.Kind == graph.KindSession || !r.drawn[c.Key] {
			continue
		}
		if !found || rank(c) < rank(best) || (rank(c) == rank(best) && graph.KeyLess(c.Key, best.Key)) {
			best, found = c, true
		}
	}
	return best.Key
}

// rank はタスクのノードを選ぶ順。進行中、未着手、完了の順。
func rank(c graph.Card) int {
	switch c.Status {
	case "in_progress":
		return 0
	case "completed":
		return 2
	default:
		return 1
	}
}

// Repos は repo: の宛先のパス。宛先の repo を依存図の repos に載せ、アプリが絞り込めるようにする。
func Repos(memos []parking.Item) []string {
	var out []string
	for _, m := range memos {
		if _, repo := parking.Destination(m.HandedTo); repo != "" {
			out = append(out, repo)
		}
	}
	return out
}

// Sort は渡したメモを最後に置いた時刻の新しい順、同じなら id の順に並べる。メモの sources は新しい順に並んでいる前提。
func Sort(memos []protocol.HandedMemo) {
	sort.SliceStable(memos, func(i, j int) bool {
		a, b := lastSourceAt(memos[i].Memo), lastSourceAt(memos[j].Memo)
		if !a.Equal(b) {
			return a.After(b)
		}
		return memos[i].Memo.ID < memos[j].Memo.ID
	})
}

func lastSourceAt(c protocol.ParkingCard) time.Time {
	if len(c.Sources) == 0 {
		return time.Time{}
	}
	return c.Sources[0].At
}
