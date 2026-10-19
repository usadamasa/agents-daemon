package board

import (
	"sort"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/dag"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/parking"
)

// ノードの状態。語は OSC 7501 の state / kind と Task 系ツールの status に合わせる (#134)。
// cc-premise: 2.1.302 osc7501 agent_status は idle / working / blocked / done / error、blocked の kind は permission / question / auth
const (
	StateWorking    = "working"
	StateDone       = "done"
	StateQuestion   = "question"
	StatePermission = "permission"
	StateError      = "error"
	StateLimit      = "limit" // daemon が上限の解除を待っている (OSC 7501 には無い)
	StatePending    = "pending"
	StateCompleted  = "completed"
	StateExited     = "exited" // セッションが終了していて、未完了のまま残った
)

// KindRef は辺の端点の参照だけがあるノード (まだ起きていないセッション、issue / PR、タスクを作らずに終了したセッション)。
const KindRef = "ref"

// 辺の出どころ。
const (
	LinkTask = "task" // 同じセッションの Task の blockedBy
	LinkEdge = "edge" // 渡す側が `agents-daemon edge add` で書いた辺 (internal/edge)
)

// stuck は止まっているノードの状態 (人の手か時間を待っている、または失敗した)。
var stuck = map[string]bool{StateQuestion: true, StatePermission: true, StateLimit: true, StateError: true}

// Graph はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つで、同じセッションの blockedBy と
// edge list の辺を 1 つにする。mod は Nodes の順に根を描き、Links から木と段を組む。
type Graph struct {
	// Nodes は下流の多い順 (同じなら Key の順)。
	Nodes []Node `json:"nodes"`
	// Links は before、after の Key の順。
	Links []Link `json:"links"`
	// LongestChain は下流の最も多い止まっているノードから、下流をたどった最長の鎖の Key。止まっているノードが無ければ空。
	LongestChain []string `json:"longest_chain"`
}

// Node はノード 1 つ。
type Node struct {
	// Key はタスクのカードと同じ Key。置き場のノードは ref: に参照を続けたもの。
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Title string `json:"title"`
	// Detail は状態の補足 (上限の解除時刻)。
	Detail string `json:"detail,omitempty"`
	// Ref は辺の端点に書く参照 (task:<sid>/<tid>、session:<名前>)。端点にできないノード (TodoWrite の項目、
	// 名前の無いセッション) は空。CWD はその端点の cwd。
	Ref       string `json:"ref,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Session   string `json:"session,omitempty"`
	Live      bool   `json:"live"`
	// Depth は段 (前提をたどった最長の深さ)。前提の無いノードは 0。
	Depth int `json:"depth"`
	// Downstream は下流 (このノードが済まないと始められないノード) の数。
	Downstream int `json:"downstream"`
	// Parked はこのタスクの途中で置かれた open のメモの数。
	Parked int `json:"parked"`
	// BlockedBy は上流をたどった原因の鎖 (近い順)。完了していない上流を、止まっているもの、進行中のものの順に選ぶ。
	BlockedBy []string `json:"blocked_by"`
	// Deadlock は待ちが輪になっていて、たどると自分へ戻ってくるか。
	Deadlock bool `json:"deadlock"`
}

// Link は辺 1 本 (before が済まないと after を始められない)。
type Link struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Kind   string `json:"kind"`
	// Edge は kind edge の辺の元の記録。mod が `edge rm` に渡す。1 本の記録が複数の辺になることがある
	// (session: の端点が根や末端のタスクを複数持つとき)。
	Edge *EdgeRecord `json:"edge,omitempty"`
}

// EdgeRecord は辺の元の記録 (internal/edge の Edge のうち mod が使う分)。
type EdgeRecord struct {
	ID     string        `json:"id"`
	Before edge.Endpoint `json:"before"`
	After  edge.Endpoint `json:"after"`
	Note   string        `json:"note,omitempty"`
}

// graphBuilder はグラフを組む途中の状態。
type graphBuilder struct {
	in    Input
	panes map[string]*daemon.PaneStatus
	nodes map[string]*Node
	// bySession はセッションのタスク (と TodoWrite の項目、タスクの無いセッション) のノードの Key。
	bySession map[string][]string
	links     []Link
	seen      map[[2]string]bool
}

// buildGraph はカードからノードを、blockedBy と辺の記録から Links を組み、段・下流・鎖を数える。
func buildGraph(in Input, cards []placed, panes map[string]*daemon.PaneStatus, names map[string]string) Graph {
	g := &graphBuilder{in: in, panes: panes, nodes: map[string]*Node{}, bySession: map[string][]string{}, seen: map[[2]string]bool{}}
	for _, c := range cards {
		g.addCard(c, names[c.card.SessionID])
	}
	g.linkBlockedBy()
	g.linkEdges()
	return g.finish()
}

func (g *graphBuilder) addCard(c placed, session string) {
	n := &Node{Key: c.card.Key, Kind: c.card.Kind, Title: c.card.Subject, CWD: c.cwd, SessionID: c.card.SessionID, Session: session, Live: c.card.Live}
	n.State, n.Detail = nodeState(c.status, c.pane)
	switch c.card.Kind {
	case KindTask:
		n.Ref = "task:" + c.card.SessionID + "/" + c.card.TaskID
	case KindSession:
		if name := g.in.SessionNames[c.card.SessionID]; name != "" {
			n.Ref = "session:" + name
		}
	}
	g.nodes[n.Key] = n
	g.bySession[n.SessionID] = append(g.bySession[n.SessionID], n.Key)
}

// nodeState はタスクの status とセッションの pane からノードの状態を決める。上限待ちは agent の自己申告より
// 先に見る (mark と同じ順)。
func nodeState(status string, p *daemon.PaneStatus) (state, detail string) {
	switch {
	case status == "completed":
		return StateCompleted, ""
	case p == nil:
		return StateExited, ""
	case status != "in_progress":
		return StatePending, ""
	case p.MonitorStatus == "waiting":
		if p.WaitUntil != nil {
			detail = p.WaitUntil.Local().Format("15:04")
		}
		return StateLimit, detail
	}
	switch multiplexer.AgentStatus(p.AgentStatus) {
	case multiplexer.AgentStatusBlocked:
		// permission 以外の待ち (auth、kind の無い dialog) も人の手を待つものとして permission に寄せる。
		if p.AgentKind == "question" {
			return StateQuestion, ""
		}
		return StatePermission, ""
	case multiplexer.AgentStatusError:
		return StateError, ""
	case multiplexer.AgentStatusDone, multiplexer.AgentStatusIdle:
		return StateDone, ""
	default:
		return StateWorking, ""
	}
}

// linkBlockedBy は同じセッションの blockedBy を辺にする。記録に無い blocker は描かない。
func (g *graphBuilder) linkBlockedBy() {
	for _, t := range g.in.Tasks {
		for _, id := range t.BlockedBy {
			g.link(t.SessionID+"/"+id, t.SessionID+"/"+t.ID, LinkTask, nil)
		}
	}
}

// linkEdges は辺の記録の端点をノードに解決して辺にする。before 側は末端、after 側は根のタスクにつなぐ。
func (g *graphBuilder) linkEdges() {
	for _, e := range g.in.Edges {
		befores := g.resolve(e.Before, g.sinks)
		afters := g.resolve(e.After, g.roots)
		via := &EdgeRecord{ID: e.ID, Before: e.Before, After: e.After, Note: e.Note}
		for _, b := range befores {
			for _, a := range afters {
				g.link(b, a, LinkEdge, via)
			}
		}
	}
}

// link は before → after を足す。どちらかのノードが無い辺、自分自身への辺、既にある辺は足さない。
func (g *graphBuilder) link(before, after, kind string, via *EdgeRecord) {
	pair := [2]string{before, after}
	if g.nodes[before] == nil || g.nodes[after] == nil || before == after || g.seen[pair] {
		return
	}
	g.seen[pair] = true
	g.links = append(g.links, Link{Before: before, After: after, Kind: kind, Edge: via})
}

// resolve は端点の参照をノードの Key にする。session: はセッションのノードのうち pick (根か末端) が選んだもの。
// ノードが無い参照は置き場のノードを作る。
func (g *graphBuilder) resolve(ep edge.Endpoint, pick func(sessionID string) []string) []string {
	kind, rest, _ := strings.Cut(ep.Ref, ":")
	switch kind {
	case "task":
		return []string{rest}
	case "session":
		id := g.latestSession(rest)
		if id == "" {
			return []string{g.placeholder(ep, rest, "", StatePending)}
		}
		if keys := pick(id); len(keys) > 0 {
			return keys
		}
		// タスクを作らずに終了したセッション。辺は残し、下流が止まっていることを見せる。
		return []string{g.placeholder(ep, rest, id, StateExited)}
	default:
		return []string{g.placeholder(ep, ep.Ref, "", StatePending)}
	}
}

// placeholder は参照を鍵にした置き場のノードを作り (既にあればそのまま)、Key を返す。
func (g *graphBuilder) placeholder(ep edge.Endpoint, title, sessionID, state string) string {
	key := "ref:" + ep.Ref
	if g.nodes[key] == nil {
		n := &Node{Key: key, Kind: KindRef, State: state, Title: title, Ref: ep.Ref, CWD: ep.CWD, SessionID: sessionID}
		if sessionID != "" {
			n.Session = title
		}
		g.nodes[key] = n
	}
	return key
}

// latestSession は名前のセッションのうち最後に動いたもの (pane があればそれ、無ければタスクの更新が新しいもの)。
// その名前のセッションが無ければ空。
func (g *graphBuilder) latestSession(name string) string {
	var best string
	var bestAt time.Time
	bestLive := false
	ids := make([]string, 0)
	for id, n := range g.in.SessionNames {
		if n == name {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		live, at := g.panes[id] != nil, g.lastUpdate(id)
		if best == "" || (live && !bestLive) || (live == bestLive && at.After(bestAt)) {
			best, bestAt, bestLive = id, at, live
		}
	}
	return best
}

func (g *graphBuilder) lastUpdate(sessionID string) time.Time {
	var at time.Time
	for _, t := range g.in.Tasks {
		if t.SessionID == sessionID && t.UpdatedAt.After(at) {
			at = t.UpdatedAt
		}
	}
	for _, l := range g.in.Todos {
		if l.SessionID == sessionID && l.UpdatedAt.After(at) {
			at = l.UpdatedAt
		}
	}
	return at
}

// roots はセッションのノードのうち、同じセッションの blockedBy で止められていないもの。
func (g *graphBuilder) roots(sessionID string) []string {
	return g.sessionEnds(sessionID, func(l Link) string { return l.After })
}

// sinks はセッションのノードのうち、同じセッションのどのノードも止めていないもの。
func (g *graphBuilder) sinks(sessionID string) []string {
	return g.sessionEnds(sessionID, func(l Link) string { return l.Before })
}

func (g *graphBuilder) sessionEnds(sessionID string, side func(Link) string) []string {
	inner := map[string]bool{}
	for _, l := range g.links {
		if l.Kind == LinkTask {
			inner[side(l)] = true
		}
	}
	var out []string
	for _, key := range g.bySession[sessionID] {
		if !inner[key] {
			out = append(out, key)
		}
	}
	return out
}

// finish は段・下流・原因の鎖・メモの数を数え、ノードと辺を並べる。
func (g *graphBuilder) finish() Graph {
	pairs := make([][2]string, 0, len(g.links))
	for _, l := range g.links {
		pairs = append(pairs, [2]string{l.Before, l.After})
	}
	a := dag.New(pairs, keyLess)
	open := func(key string) bool { return g.nodes[key].State != StateCompleted }
	rank := func(key string) int { return rank(g.nodes[key].State) }
	out := Graph{Nodes: make([]Node, 0, len(g.nodes)), Links: g.links, LongestChain: []string{}}
	for key, n := range g.nodes {
		down, cyclic := a.Reach(key)
		n.Depth = a.Depth(key)
		n.Downstream = len(down)
		n.Deadlock = cyclic
		n.BlockedBy = a.Cause(key, open, rank)
		n.Parked = parked(*n, g.in.Parking)
		out.Nodes = append(out.Nodes, *n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].Downstream != out.Nodes[j].Downstream {
			return out.Nodes[i].Downstream > out.Nodes[j].Downstream
		}
		return keyLess(out.Nodes[i].Key, out.Nodes[j].Key)
	})
	if out.Links == nil {
		out.Links = []Link{}
	}
	sort.Slice(out.Links, func(i, j int) bool {
		if out.Links[i].Before != out.Links[j].Before {
			return keyLess(out.Links[i].Before, out.Links[j].Before)
		}
		return keyLess(out.Links[i].After, out.Links[j].After)
	})
	for _, n := range out.Nodes {
		if stuck[n.State] {
			out.LongestChain = a.Longest(n.Key)
			break
		}
	}
	return out
}

// rank は原因の鎖で先に選ぶ上流の順。止まっているもの、進行中のもの、それ以外。
func rank(state string) int {
	switch {
	case stuck[state]:
		return 0
	case state == StateWorking:
		return 1
	default:
		return 2
	}
}

// parked はノードのタスクの途中で置かれた open のメモの数。タスクを持たないノードは、タスクを指さずに
// そのセッションが置いたメモを数える。
func parked(n Node, memos []parking.Item) int {
	if n.SessionID == "" || n.Kind == KindTodo {
		return 0
	}
	taskID := ""
	if n.Kind == KindTask {
		_, taskID, _ = strings.Cut(strings.TrimPrefix(n.Ref, "task:"), "/")
	}
	count := 0
	for _, m := range memos {
		for _, s := range m.Sources {
			if s.SessionID == n.SessionID && s.TaskID == taskID {
				count++
				break
			}
		}
	}
	return count
}
