// Package graph はタスクの記録と生きているセッションから、セッションをまたぐタスクの依存の DAG を組む (#134)。
// ノードの材料 (Card) と repo ごとのセッションを組むのは deps パッケージ、段・下流・待ちの輪・鎖の数え方とレーンの
// 割り当ては graph/dag が持つ。
package graph

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/deps/graph/dag"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// ノードの種類。KindRef 以外は Card の種類と同じ。
const (
	KindTask    = "task"    // TaskCreate で作られたタスク
	KindTodo    = "todo"    // TodoWrite のリストの 1 項目
	KindSession = "session" // タスクを作っていない生きているセッション (subject は今の作業か、セッションの名前)
	// KindRef は辺の端点の参照だけがあるノード (まだ起きていないセッション、issue / PR、タスクを作らずに終了したセッション)。
	// Key は端点の参照そのもの。終了したセッションは名前で引いても sid:<session_id> を Key にし、名前と session ID の
	// どちらで書いた辺も同じノードに集める。
	KindRef = "ref"
)

// ノードの状態。語は OSC 7501 の state / kind と Task 系ツールの status に合わせる (#134)。working から unknown の
// 7 つ (pending、completed、exited を除く) はセッションの活動 (Card.Activity) の語も兼ねる。pane から活動を導くのは
// deps パッケージの activity。
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
	// StateExternal は daemon が状態を知らない外部の参照 (issue / PR)。済んだかどうか分からないので、原因の鎖に
	// 選ばず、末端の完了ノードを残す上流にも数えない。
	StateExternal = "external"
	// StateUnknown はセッションは生きているが状態を分類できない (申告の前、古い record)。作業中とは限らないので
	// working に倒さない。
	StateUnknown = "unknown"
)

// 辺の出どころ。
const (
	LinkTask = "task" // 同じセッションの Task の blockedBy
	LinkEdge = "edge" // 渡す側が `agents-daemon edge add` で書いた辺 (edge)
)

// halted はセッション自身が止まっているノードの状態 (人の手か時間を待っている、または失敗した)。
var halted = map[string]bool{StateQuestion: true, StatePermission: true, StateLimit: true, StateError: true}

// DAG の形は protocol の schema が持つ。組み方の規則はここが持つ:
//   - ノードはタスク 1 つで、同じセッションの blockedBy と edge list の辺を 1 つにする
//   - Nodes は下流の多い順 (同じなら Key の順)、Links は before、after の Key の順
//   - LongestChain は止まっているノード (待ちの輪を含む) から数え、止まっているノードが無ければ空
//   - Rows は Nodes の順を優先の順にした位相順で、edge の辺 (セッションをまたぐ) は破線 (graph/dag の Rows)
//   - BlockedBy は完了していない上流を、止まっているもの、進行中のものの順に選ぶ。Memos は消えたメモの id を落とす
//   - before 側の sid: の端点が末端のタスクを複数持つと、末端をセッションのノード (sid:) に集め、辺の記録は
//     そのノードからの 1 本にする (#405)。after 側の sid: は根のタスクの数だけの Link になる
type (
	// Graph はセッションをまたぐタスクの依存の DAG (#134)。
	Graph = protocol.DepsGraph
	// Node はノード 1 つ。
	Node = protocol.GraphNode
	// MemoRef はタスクが扱うメモ 1 つ。
	MemoRef = protocol.MemoRef
)

// Card はノードにする作業 1 つ (タスク、TodoWrite の項目、タスクの無い生きているセッション)。
type Card struct {
	// Key は依存図で一意な識別子 (task:、todo:、sid: の Ref)。
	Key  string
	Kind string
	// TaskID と Description は kind task のタスクの ID と description。
	TaskID      string
	Description string
	Title       string
	// Summary は kind session の Card の、Claude Code が申告した今の作業。
	Summary   string
	SessionID string
	// Session はセッションの表示名。CWD は作業の cwd (タスクの記録の cwd、無ければ pane の cwd)。
	Session string
	CWD     string
	Live    bool
	// Status はタスクの status (タスクの無いセッションは in_progress)。
	Status string
	// Activity はセッションの活動 (StateWorking、StateDone、StateQuestion、StatePermission、StateError、StateLimit、
	// StateUnknown のどれか)。生きていなければ空。Detail はその補足 (上限の解除時刻)。
	Activity string
	Detail   string
}

// Input はグラフを組む材料。
type Input struct {
	Cards []Card
	Tasks []record.Task
	Todos []record.TodoList
	// Edges はセッションをまたぐ辺 (edge)。
	Edges []edge.Edge
	// Memos はメモ (status を問わない)。タスクの description との照合に使う。Open はそのうち open のもの。
	Memos []parking.Item
	Open  []parking.Item
	// SessionNames は session ID → セッション名 (statusline の session_name)。ノードの表示名と題に使う。
	SessionNames map[string]string
	// PeerNames は session ID → ListAgents の名前 (台帳)。ノードの SessionName (SendMessage の宛先に書く形) に使う。
	PeerNames map[string]string
}

// builder はグラフを組む途中の状態。
type builder struct {
	in    Input
	nodes map[string]*Node
	// bySession はセッションのタスク (と TodoWrite の項目、タスクの無いセッション) のノードの Key。
	bySession map[string][]string
	// activity はセッションが生きていればその活動 (Card.Activity)。末端を集めるセッションのノードの状態に使う。
	activity map[string]string
	links    []protocol.GraphLink
	seen     map[[2]string]bool
}

// Build は Cards からノードを、blockedBy と辺の記録から Links を組み、段・下流・鎖を数える。
func Build(in Input) Graph {
	g := &builder{in: in, nodes: map[string]*Node{}, bySession: map[string][]string{}, activity: map[string]string{}, seen: map[[2]string]bool{}}
	for _, c := range in.Cards {
		g.addCard(c)
	}
	g.linkBlockedBy()
	g.linkEdges()
	return g.finish()
}

func (g *builder) addCard(c Card) {
	n := &Node{Key: c.Key, Kind: c.Kind, Title: c.Title, Summary: c.Summary, Endpoint: c.Kind != KindTodo, CWD: c.CWD, TaskID: c.TaskID, Description: c.Description, SessionID: c.SessionID,
		Session: c.Session, SessionName: g.in.PeerNames[c.SessionID], Live: c.Live, Memos: []MemoRef{}}
	n.State = nodeState(c.Status, c.Live, c.Activity)
	if n.State == StateLimit {
		n.Detail = c.Detail
	}
	g.nodes[n.Key] = n
	g.bySession[n.SessionID] = append(g.bySession[n.SessionID], n.Key)
	if c.Live {
		g.activity[c.SessionID] = c.Activity
	}
}

// nodeState は作業の進み (タスクの status)、セッションが生きているか、セッションの活動からノードの状態を決める。
// 進行中の作業だけがセッションの活動をそのまま映す。
func nodeState(progress string, live bool, activity string) string {
	switch {
	case progress == "completed":
		return StateCompleted
	case !live:
		return StateExited
	case progress != "in_progress":
		return StatePending
	default:
		return activity
	}
}

// markStuck は止まっているかを付ける。Halted はセッション自身が人の手か時間を待っているか失敗した状態、Stuck は
// それに完了していない待ちの輪を足したもの (輪も人が解かないと進まない)。
func markStuck(n *Node) {
	n.Halted = halted[n.State]
	n.Stuck = n.Halted || (n.Deadlock && n.State != StateCompleted)
}

// linkBlockedBy は同じセッションの blockedBy を辺にする。記録に無い blocker は描かない。
func (g *builder) linkBlockedBy() {
	for _, t := range g.in.Tasks {
		for _, id := range t.BlockedBy {
			g.link(ref.Task(t.SessionID, id), ref.Task(t.SessionID, t.ID), LinkTask, nil)
		}
	}
}

// linkEdges は辺の記録の端点をノードに解決して辺にする。before 側は末端 (複数ならセッションのノード)、after 側は
// 根のタスクにつなぐ。
func (g *builder) linkEdges() {
	for _, e := range g.in.Edges {
		befores := g.resolve(e.Before, g.sinkHub)
		afters := g.resolve(e.After, g.roots)
		via := &protocol.EdgeRecord{ID: e.ID, Before: e.Before, After: e.After, Note: e.Note}
		for _, b := range befores {
			for _, a := range afters {
				g.link(b, a, LinkEdge, via)
			}
		}
	}
}

// link は before → after を足す。どちらかのノードが無い辺、自分自身への辺、既にある辺は足さない。
func (g *builder) link(before, after, kind string, via *protocol.EdgeRecord) {
	pair := [2]string{before, after}
	if g.nodes[before] == nil || g.nodes[after] == nil || before == after || g.seen[pair] {
		return
	}
	g.seen[pair] = true
	g.links = append(g.links, protocol.GraphLink{Before: before, After: after, Kind: kind, Edge: via})
}

// resolve は端点の参照をノードの Key にする。sid: はセッションのノードのうち pick (根か末端) が選んだもの。
// session:<名前> は daemon がまだセッションに結びつけていない端点で (edge の Bind)、名前の置き場の
// ノードにする。ノードが無い参照は置き場のノードを作る。
func (g *builder) resolve(ep edge.Endpoint, pick func(sessionID string) []string) []string {
	kind, rest, _ := strings.Cut(ep.Ref, ":")
	switch kind {
	case "task":
		return []string{ep.Ref}
	case "session":
		return []string{g.placeholder(ep.Ref, ep.CWD, rest, "", StatePending)}
	case "sid":
		return g.sessionKeys(rest, ep.CWD, pick)
	}
	key := g.placeholder(ep.Ref, ep.CWD, ep.Ref, "", StatePending)
	if it, ok := ref.ParseItem(ep.Ref); ok {
		n := g.nodes[key]
		n.Title, n.State, n.RefKind = it.Title(), StateExternal, it.Kind
	}
	return []string{key}
}

// sessionKeys はセッションのノードのうち pick が選んだものの Key。ノードが無ければ sid: の置き場のノードを作る。
func (g *builder) sessionKeys(sessionID, cwd string, pick func(sessionID string) []string) []string {
	if keys := pick(sessionID); len(keys) > 0 {
		return keys
	}
	// タスクがすべて輪になっていて根も末端も無い。
	if keys := g.bySession[sessionID]; len(keys) > 0 {
		return keys
	}
	// タスクを作らずに終了したセッション。辺は残し、下流が止まっていることを見せる。
	key := ref.SessionID(sessionID)
	title := g.in.SessionNames[sessionID]
	if title == "" {
		title = key
	}
	return []string{g.placeholder(key, cwd, title, sessionID, StateExited)}
}

// placeholder は参照を Key にした置き場のノードを作り (既にあればそのまま)、Key を返す。
func (g *builder) placeholder(key, cwd, title, sessionID, state string) string {
	if g.nodes[key] == nil {
		n := &Node{Key: key, Kind: KindRef, State: state, Title: title, Endpoint: true, CWD: cwd, SessionID: sessionID, Memos: []MemoRef{}}
		if sessionID != "" {
			n.Session, n.SessionName = g.in.SessionNames[sessionID], g.in.PeerNames[sessionID]
		}
		g.nodes[key] = n
	}
	return key
}

// roots はセッションのノードのうち、同じセッションの blockedBy で止められていないもの。
func (g *builder) roots(sessionID string) []string {
	return g.sessionEnds(sessionID, func(l protocol.GraphLink) string { return l.After })
}

// sinkHub は before 側の sid: の端点のノード。末端が 1 つならそのノード、複数あるなら末端 (輪で末端が無ければ
// セッションのノード全部) を sid:<id> のノードに集めてそれを返す。どの末端を待つ辺かは記録に無いので、末端の全部と
// 組み合わせると辺の記録 1 つが末端の数だけ線になる (#405)。
func (g *builder) sinkHub(sessionID string) []string {
	key := ref.SessionID(sessionID)
	if g.nodes[key] != nil {
		// 集めたノード、タスクの無い生きているセッション、終了した置き場のどれか。
		return []string{key}
	}
	keys := g.sinks(sessionID)
	if len(keys) == 0 {
		keys = g.bySession[sessionID]
	}
	if len(keys) <= 1 {
		return keys
	}
	state := StateCompleted
	for _, k := range keys {
		if g.nodes[k].State != StateCompleted {
			state = nodeState("in_progress", g.activity[sessionID] != "", g.activity[sessionID])
			break
		}
	}
	title := g.in.SessionNames[sessionID]
	if title == "" {
		title = key
	}
	hub := g.placeholder(key, g.nodes[keys[0]].CWD, title, sessionID, state)
	for _, k := range keys {
		g.link(k, hub, LinkTask, nil)
	}
	return []string{hub}
}

// sinks はセッションのノードのうち、同じセッションのどのノードも止めていないもの。
func (g *builder) sinks(sessionID string) []string {
	return g.sessionEnds(sessionID, func(l protocol.GraphLink) string { return l.Before })
}

func (g *builder) sessionEnds(sessionID string, side func(protocol.GraphLink) string) []string {
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
func (g *builder) finish() Graph {
	pairs := make([][2]string, 0, len(g.links))
	for _, l := range g.links {
		pairs = append(pairs, [2]string{l.Before, l.After})
	}
	a := dag.New(pairs, KeyLess)
	// 原因の鎖に選ぶ上流。外部の参照は済んだか分からないので選ばない。
	open := func(key string) bool { s := g.nodes[key].State; return s != StateCompleted && s != StateExternal }
	rankOf := func(key string) int { return rank(g.nodes[key].State) }
	out := Graph{Nodes: make([]Node, 0, len(g.nodes)), Links: g.links, LongestChain: []string{}}
	memos := g.mentionedMemos()
	for key, n := range g.nodes {
		down, cyclic := a.Reach(key)
		n.Depth = a.Depth(key)
		n.Downstream = len(down)
		n.Deadlock = cyclic
		markStuck(n)
		n.BlockedBy = a.Cause(key, open, rankOf)
		n.Parked = parked(*n, g.in.Open)
		n.Memos = append(n.Memos, memos[key]...)
		out.Nodes = append(out.Nodes, *n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].Downstream != out.Nodes[j].Downstream {
			return out.Nodes[i].Downstream > out.Nodes[j].Downstream
		}
		return KeyLess(out.Nodes[i].Key, out.Nodes[j].Key)
	})
	if out.Links == nil {
		out.Links = []protocol.GraphLink{}
	}
	sort.Slice(out.Links, func(i, j int) bool {
		if out.Links[i].Before != out.Links[j].Before {
			return KeyLess(out.Links[i].Before, out.Links[j].Before)
		}
		return KeyLess(out.Links[i].After, out.Links[j].After)
	})
	for _, n := range out.Nodes {
		if n.Stuck {
			out.LongestChain = a.Longest(n.Key)
			break
		}
	}
	order := make([]string, 0, len(out.Nodes))
	for _, n := range out.Nodes {
		order = append(order, n.Key)
	}
	var folded *dag.Graph
	var keys []string
	out.Folded, folded, keys = g.fold(a, order, pairs)
	out.Rows, out.RowsWithCompleted = g.rows(a, order, folded, keys, out.Folded.Links, out.LongestChain)
	return out
}

// fold は完了したノードを隠した図を組む (#392)。隠したノードを通り抜けていた辺は畳んで残し、段はこの図の辺で
// 数え直す (#395。全部の図の段のままだと、隠したノードの段が空いて線が伸びる)。図の表と見せるノードの key も返す。
func (g *builder) fold(a *dag.Graph, order []string, pairs [][2]string) (protocol.FoldedGraph, *dag.Graph, []string) {
	shown := map[string]bool{}
	keys := make([]string, 0, len(order))
	for _, key := range order {
		if g.shownWhenFolded(a, key) {
			shown[key] = true
			keys = append(keys, key)
		}
	}
	links := a.Fold(order, func(key string) bool { return shown[key] })
	if links == nil {
		links = []protocol.FoldedLink{}
	}
	shownPairs := make([][2]string, 0, len(pairs)+len(links))
	for _, p := range pairs {
		if shown[p[0]] && shown[p[1]] {
			shownPairs = append(shownPairs, p)
		}
	}
	for _, l := range links {
		shownPairs = append(shownPairs, [2]string{l.Before, l.After})
	}
	folded := dag.New(shownPairs, KeyLess)
	nodes := make([]protocol.FoldedNode, 0, len(keys))
	for _, key := range keys {
		nodes = append(nodes, protocol.FoldedNode{Key: key, Depth: folded.Depth(key)})
	}
	return protocol.FoldedGraph{Nodes: nodes, Links: links}, folded, keys
}

// shownWhenFolded は完了したノードを隠した図にノードを見せるか。完了したノードは隠すが、下流に完了していないノードが
// 無く、完了も終了もしていない上流を直接持つものは残す。peer に渡した作業の報告を受けて完了にしたタスクがこれで、
// 隠すと peer が線を持たずに浮く。上流が完了・終了したら隠す。
func (g *builder) shownWhenFolded(a *dag.Graph, key string) bool {
	if g.nodes[key].State != StateCompleted {
		return true
	}
	down, _ := a.Reach(key)
	for k := range down {
		if g.nodes[k].State != StateCompleted {
			return false
		}
	}
	for _, l := range g.links {
		if l.After != key {
			continue
		}
		if s := g.nodes[l.Before].State; s != StateCompleted && s != StateExited && s != StateExternal {
			return true
		}
	}
	return false
}

// rows はレーンの行を 2 組組む: 完了したノードを隠した図 (folded、keys はその見せるノード) と、全部を含むもの。
// 隠した方は辺を絞って組み直した表を使う (Depth で輪を切る位置が辺の集合で変わるので、全部の表を使い回さない)。
// 畳んだ辺は破線にする。longest chain の辺には印を付ける。
func (g *builder) rows(all *dag.Graph, order []string, folded *dag.Graph, keys []string, foldedLinks []protocol.FoldedLink, chain []string) (opened, every []dag.Row) {
	kinds := map[[2]string]string{}
	for _, l := range g.links {
		kinds[[2]string{l.Before, l.After}] = l.Kind
	}
	for _, l := range foldedLinks {
		kinds[[2]string{l.Before, l.After}] = LinkEdge
	}
	dashed := func(before, after string) bool { return kinds[[2]string{before, after}] == LinkEdge }
	onChain := map[[2]string]bool{}
	for i := 1; i < len(chain); i++ {
		onChain[[2]string{chain[i-1], chain[i]}] = true
	}
	highlight := func(before, after string) bool { return onChain[[2]string{before, after}] }
	return folded.Rows(keys, dashed, highlight), all.Rows(order, dashed, highlight)
}

// rank は原因の鎖で先に選ぶ上流の順。止まっているもの、進行中のもの、それ以外。
func rank(state string) int {
	switch {
	case halted[state]:
		return 0
	case state == StateWorking:
		return 1
	default:
		return 2
	}
}

// mentionedMemos はタスクのノードの Key → description に id が書かれたメモ (status を問わない)。
func (g *builder) mentionedMemos() map[string][]MemoRef {
	out := map[string][]MemoRef{}
	for _, t := range g.in.Tasks {
		key := ref.Task(t.SessionID, t.ID)
		for _, m := range parking.Mentioned(g.in.Memos, t.Description) {
			out[key] = append(out[key], MemoRef{ID: m.ID, Title: m.Title, Status: m.Status})
		}
	}
	return out
}

// parked はノードのタスクの途中で置かれたメモの数。タスクを持たないノードは、タスクを指さずに
// そのセッションが置いたメモを数える。
func parked(n Node, memos []parking.Item) int {
	if n.SessionID == "" || n.Kind == KindTodo {
		return 0
	}
	return len(parking.PlacedDuring(memos, n.SessionID, n.TaskID))
}

// KeyLess は Key を比べる。同じセッションの task ID と TodoWrite の項目の番号は数字なので、"10" が "9" より後に
// なるように数で比べる。
func KeyLess(a, b string) bool {
	da, ia := filepath.Split(a)
	db, ib := filepath.Split(b)
	if da == db {
		na, errA := strconv.Atoi(ia)
		nb, errB := strconv.Atoi(ib)
		if errA == nil && errB == nil {
			return na < nb
		}
	}
	return a < b
}
