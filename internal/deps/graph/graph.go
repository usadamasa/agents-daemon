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

// Graph はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つで、同じセッションの blockedBy と
// edge list の辺を 1 つにする。mod は Nodes の順に根を描き、Links から木と段を組む。
type Graph struct {
	// Nodes は下流の多い順 (同じなら Key の順)。
	Nodes []Node `json:"nodes"`
	// Links は before、after の Key の順。
	Links []Link `json:"links"`
	// LongestChain は下流の最も多い止まっているノード (待ちの輪を含む) から、下流をたどった最長の鎖の Key。
	// 止まっているノードが無ければ空。
	LongestChain []string `json:"longest_chain"`
	// Rows は pane に描くレーンの行 (graph/dag の Rows)。完了したノードとその辺を落として組む。
	// 行の並びは Nodes の順を優先の順にした位相順。edge の辺 (セッションをまたぐ) は破線。
	Rows []dag.Row `json:"rows"`
	// RowsWithCompleted は完了したノードも含めて組んだ行 (Show completed)。
	RowsWithCompleted []dag.Row `json:"rows_with_completed"`
}

// Node はノード 1 つ。
type Node struct {
	// Key はノードの Ref (ref)。
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Title string `json:"title"`
	// Detail は状態の補足 (上限の解除時刻)。
	Detail string `json:"detail,omitempty"`
	// Endpoint は Key をそのまま辺の端点に書けるか (TodoWrite の項目は書けない)。CWD はその端点の cwd。
	Endpoint bool   `json:"endpoint"`
	CWD      string `json:"cwd,omitempty"`
	// TaskID は kind task のノードのタスクの ID (TaskUpdate に書く形)。Description はそのタスクの description
	// (pane が自分のタスクをほかのセッションへ渡す本文)。
	TaskID      string `json:"task_id,omitempty"`
	Description string `json:"description,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	// Session は表示名 (同名のセッションがあれば session ID の先頭が付く)。SessionName はセッション名
	// そのもの (`claude --name`、SendMessage の宛先に書く形)。名前が無ければ空。
	Session     string `json:"session,omitempty"`
	SessionName string `json:"session_name,omitempty"`
	Live        bool   `json:"live"`
	// Depth は段 (前提をたどった最長の深さ)。前提の無いノードは 0。
	Depth int `json:"depth"`
	// Downstream は下流 (このノードが済まないと始められないノード) の数。
	Downstream int `json:"downstream"`
	// Parked はこのタスクの途中で置かれた open のメモの数。
	Parked int `json:"parked"`
	// Memos はこのタスクが扱うメモ (description に id が書かれたもの、書かれた順)。status を問わず照合し、
	// 消えたメモの id は落とす。
	Memos []MemoRef `json:"memos"`
	// BlockedBy は上流をたどった原因の鎖 (近い順)。完了していない上流を、止まっているもの、進行中のものの順に選ぶ。
	BlockedBy []string `json:"blocked_by"`
	// Deadlock は待ちが輪になっていて、たどると自分へ戻ってくるか。
	Deadlock bool `json:"deadlock"`
	// Halted はセッション自身が止まっているか (question、permission、limit、error)。Stuck は止まっているノードか
	// (Halted か、完了していない待ちの輪)。mod はこの 2 つで数え、色を付け、規則を持たない。
	Halted bool `json:"halted"`
	Stuck  bool `json:"stuck"`
}

// MemoRef はタスクが扱うメモ 1 つ。
type MemoRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Link は辺 1 本 (before が済まないと after を始められない)。
type Link struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Kind   string `json:"kind"`
	// Edge は kind edge の辺の元の記録。mod が `edge rm` に渡す。1 本の記録が複数の辺になることがある
	// (session: や sid: の端点が根や末端のタスクを複数持つとき)。
	Edge *EdgeRecord `json:"edge,omitempty"`
}

// EdgeRecord は辺の元の記録 (edge の Edge のうち mod が使う分)。
type EdgeRecord struct {
	ID     string        `json:"id"`
	Before edge.Endpoint `json:"before"`
	After  edge.Endpoint `json:"after"`
	Note   string        `json:"note,omitempty"`
}

// Card はノードにする作業 1 つ (タスク、TodoWrite の項目、タスクの無い生きているセッション)。
type Card struct {
	// Key は依存図で一意な識別子 (task:、todo:、sid: の Ref)。
	Key  string
	Kind string
	// TaskID と Description は kind task のタスクの ID と description。
	TaskID      string
	Description string
	Title       string
	SessionID   string
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
	// SessionNames は session ID → セッション名 (statusline の session_name)。
	SessionNames map[string]string
}

// builder はグラフを組む途中の状態。
type builder struct {
	in    Input
	nodes map[string]*Node
	// bySession はセッションのタスク (と TodoWrite の項目、タスクの無いセッション) のノードの Key。
	bySession map[string][]string
	links     []Link
	seen      map[[2]string]bool
}

// Build は Cards からノードを、blockedBy と辺の記録から Links を組み、段・下流・鎖を数える。
func Build(in Input) Graph {
	g := &builder{in: in, nodes: map[string]*Node{}, bySession: map[string][]string{}, seen: map[[2]string]bool{}}
	for _, c := range in.Cards {
		g.addCard(c)
	}
	g.linkBlockedBy()
	g.linkEdges()
	return g.finish()
}

func (g *builder) addCard(c Card) {
	n := &Node{Key: c.Key, Kind: c.Kind, Title: c.Title, Endpoint: c.Kind != KindTodo, CWD: c.CWD, TaskID: c.TaskID, Description: c.Description, SessionID: c.SessionID,
		Session: c.Session, SessionName: g.in.SessionNames[c.SessionID], Live: c.Live, Memos: []MemoRef{}}
	n.State = nodeState(c.Status, c.Live, c.Activity)
	if n.State == StateLimit {
		n.Detail = c.Detail
	}
	g.nodes[n.Key] = n
	g.bySession[n.SessionID] = append(g.bySession[n.SessionID], n.Key)
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
func (n *Node) markStuck() {
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

// linkEdges は辺の記録の端点をノードに解決して辺にする。before 側は末端、after 側は根のタスクにつなぐ。
func (g *builder) linkEdges() {
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
func (g *builder) link(before, after, kind string, via *EdgeRecord) {
	pair := [2]string{before, after}
	if g.nodes[before] == nil || g.nodes[after] == nil || before == after || g.seen[pair] {
		return
	}
	g.seen[pair] = true
	g.links = append(g.links, Link{Before: before, After: after, Kind: kind, Edge: via})
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
	default:
		return []string{g.placeholder(ep.Ref, ep.CWD, ep.Ref, "", StatePending)}
	}
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
		if name := g.in.SessionNames[sessionID]; sessionID != "" && name != "" {
			n.Session, n.SessionName = name, name
		}
		g.nodes[key] = n
	}
	return key
}

// roots はセッションのノードのうち、同じセッションの blockedBy で止められていないもの。
func (g *builder) roots(sessionID string) []string {
	return g.sessionEnds(sessionID, func(l Link) string { return l.After })
}

// sinks はセッションのノードのうち、同じセッションのどのノードも止めていないもの。
func (g *builder) sinks(sessionID string) []string {
	return g.sessionEnds(sessionID, func(l Link) string { return l.Before })
}

func (g *builder) sessionEnds(sessionID string, side func(Link) string) []string {
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
	open := func(key string) bool { return g.nodes[key].State != StateCompleted }
	rankOf := func(key string) int { return rank(g.nodes[key].State) }
	out := Graph{Nodes: make([]Node, 0, len(g.nodes)), Links: g.links, LongestChain: []string{}}
	memos := g.mentionedMemos()
	for key, n := range g.nodes {
		down, cyclic := a.Reach(key)
		n.Depth = a.Depth(key)
		n.Downstream = len(down)
		n.Deadlock = cyclic
		n.markStuck()
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
		out.Links = []Link{}
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
	out.Rows, out.RowsWithCompleted = g.rows(a, out.Nodes, pairs, open, out.LongestChain)
	return out
}

// rows はレーンの行を 2 組組む: 完了したノードとその辺を落としたものと、全部を含むもの。落とした方は辺を絞って
// 組み直す (Depth で輪を切る位置が辺の集合で変わるので、全部の表を使い回さない)。longest chain の辺には印を付ける。
func (g *builder) rows(all *dag.Graph, nodes []Node, pairs [][2]string, open func(string) bool, chain []string) (opened, every []dag.Row) {
	kinds := map[[2]string]string{}
	for _, l := range g.links {
		kinds[[2]string{l.Before, l.After}] = l.Kind
	}
	dashed := func(before, after string) bool { return kinds[[2]string{before, after}] == LinkEdge }
	onChain := map[[2]string]bool{}
	for i := 1; i < len(chain); i++ {
		onChain[[2]string{chain[i-1], chain[i]}] = true
	}
	highlight := func(before, after string) bool { return onChain[[2]string{before, after}] }
	order := make([]string, 0, len(nodes))
	openOrder := make([]string, 0, len(nodes))
	for _, n := range nodes {
		order = append(order, n.Key)
		if open(n.Key) {
			openOrder = append(openOrder, n.Key)
		}
	}
	openPairs := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		if open(p[0]) && open(p[1]) {
			openPairs = append(openPairs, p)
		}
	}
	return dag.New(openPairs, KeyLess).Rows(openOrder, dashed, highlight), all.Rows(order, dashed, highlight)
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
