package board

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/board/dag"
	"github.com/usadamasa/agents-daemon/internal/board/graph"
	"github.com/usadamasa/agents-daemon/internal/daemon/state"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// nodes はグラフのノードを Key で引けるようにする。
func nodes(g graph.Graph) map[string]graph.Node {
	m := map[string]graph.Node{}
	for _, n := range g.Nodes {
		m[n.Key] = n
	}
	return m
}

// links はグラフの辺を "before>after" の並びにする。
func links(g graph.Graph) []string {
	out := []string{}
	for _, l := range g.Links {
		out = append(out, l.Before+">"+l.After)
	}
	return out
}

// ノードはタスク 1 つ。state は完了、pane の状態 (上限待ちを先に見る)、未着手、終了したセッションの順に決める。
// タスクの無い生きているセッションは 1 ノード、TodoWrite の項目もノードになる。
func TestGraph_nodeStates(t *testing.T) {
	until := time.Date(2026, 10, 18, 14, 30, 0, 0, time.Local)
	b := build(input{
		Domain: domain.Work,
		PID:    42,
		Snapshot: snapshot(
			state.PaneStatus{PaneID: "p1", SessionID: "W", CWD: "/w/a", AgentStatus: "working"},
			state.PaneStatus{PaneID: "p2", SessionID: "D", CWD: "/w/a", AgentStatus: "done"},
			state.PaneStatus{PaneID: "p3", SessionID: "Q", CWD: "/w/a", AgentStatus: "blocked", AgentKind: "question"},
			state.PaneStatus{PaneID: "p4", SessionID: "P", CWD: "/w/a", AgentStatus: "blocked", AgentKind: "permission"},
			state.PaneStatus{PaneID: "p5", SessionID: "E", CWD: "/w/a", AgentStatus: "error"},
			state.PaneStatus{PaneID: "p6", SessionID: "L", CWD: "/w/a", AgentStatus: "error", MonitorStatus: "waiting", WaitUntil: &until},
			state.PaneStatus{PaneID: "p7", SessionID: "S", CWD: "/w/s", AgentStatus: "working", AgentMessage: "調べている"},
			state.PaneStatus{PaneID: "p8", SessionID: "T", CWD: "/w/t", AgentStatus: "working"},
			state.PaneStatus{PaneID: "p9", SessionID: "U", CWD: "/w/u", AgentStatus: "done"},
			state.PaneStatus{PaneID: "p10", SessionID: "V", CWD: "/w/dotfiles", AgentStatus: "unknown"},
			state.PaneStatus{PaneID: "p11", SessionID: "N", CWD: "/w/a", AgentStatus: "unknown"},
		),
		Tasks: []tasklog.Task{
			task("W", "1", "書く", "in_progress", "/w/a"),
			task("W", "2", "次", "pending", "/w/a"),
			task("W", "3", "済んだ", "completed", "/w/a"),
			task("D", "1", "d", "in_progress", "/w/a"),
			task("Q", "1", "q", "in_progress", "/w/a"),
			task("P", "1", "p", "in_progress", "/w/a"),
			task("E", "1", "e", "in_progress", "/w/a"),
			task("L", "1", "l", "in_progress", "/w/a"),
			task("N", "1", "n", "in_progress", "/w/a"),
			task("X", "1", "残った作業", "pending", "/w/x"),
			task("X", "2", "残った進行中", "in_progress", "/w/x"),
		},
		Todos: []tasklog.TodoList{{SessionID: "T", CWD: "/w/t", UpdatedAt: t0, Todos: []tasklog.Todo{
			{Content: "a", Status: "completed"}, {Content: "b", Status: "in_progress"},
		}}},
		SessionNames: map[string]string{"W": "writer", "U": "waiter"},
	})
	got := nodes(b.Graph)
	want := map[string]string{
		"W/1": graph.StateWorking, "W/2": graph.StatePending, "W/3": graph.StateCompleted,
		"D/1": graph.StateDone, "Q/1": graph.StateQuestion, "P/1": graph.StatePermission, "E/1": graph.StateError, "L/1": graph.StateLimit,
		"X/1": graph.StateExited, "X/2": graph.StateExited,
		"S/session": graph.StateWorking, "U/session": graph.StateDone,
		// 状態を分類できないセッション (申告の前、古い record) は working に倒さない。
		"V/session": graph.StateUnknown, "N/1": graph.StateUnknown,
		"T/todo-0": graph.StateCompleted, "T/todo-1": graph.StateWorking,
	}
	if len(got) != len(want) {
		t.Errorf("nodes = %d, want %d: %+v", len(got), len(want), got)
	}
	for key, state := range want {
		if got[key].State != state {
			t.Errorf("%s state = %q, want %q", key, got[key].State, state)
		}
	}
	w := got["W/1"]
	if w.Kind != KindTask || w.Title != "書く" || w.Ref != "task:W/1" || w.CWD != "/w/a" || w.SessionID != "W" || w.Session != "writer" || !w.Live {
		t.Errorf("W/1 = %+v", w)
	}
	if got["L/1"].Detail != "14:30" || got["Q/1"].Detail != "" {
		t.Errorf("detail = %q / %q, want 上限の解除時刻だけ", got["L/1"].Detail, got["Q/1"].Detail)
	}
	if s := got["S/session"]; s.Kind != KindSession || s.Title != "調べている" || s.Ref != "" {
		t.Errorf("S/session = %+v, want 名前の無いセッションは参照を持たない", s)
	}
	// msg の無いセッションはセッション名、名前も無ければ cwd の末尾を title にする。
	if u := got["U/session"]; u.Title != "waiter" {
		t.Errorf("U/session title = %q, want waiter", u.Title)
	}
	if v := got["V/session"]; v.Title != "dotfiles" {
		t.Errorf("V/session title = %q, want dotfiles", v.Title)
	}
	if got["T/todo-1"].Ref != "" {
		t.Errorf("todo の参照 = %q, want 空 (辺の端点にできない)", got["T/todo-1"].Ref)
	}
	if got["X/1"].Live {
		t.Errorf("X/1 は終了したセッション")
	}
}

// 同じセッションの blockedBy と、task: の参照の辺 (edge list) を 1 つの DAG にする。
// 記録に無いタスクを指す辺は描かない。下流の数と段 (前提の深さ) を数える。
func TestGraph_linksDepthAndDownstream(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		Domain: domain.Work,
		Tasks: []tasklog.Task{
			task("A", "1", "設計", "completed", cwd),
			task("A", "2", "実装", "pending", cwd, "1"),
			task("A", "3", "PR", "pending", cwd, "2"),
			task("B", "1", "リリース", "pending", cwd),
			task("B", "2", "確認", "pending", cwd, "1"),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "task:A/3", CWD: cwd}, After: edge.Endpoint{Ref: "task:B/1", CWD: cwd}, Note: "PR の後"},
			{ID: "e2", Before: edge.Endpoint{Ref: "task:Z/9", CWD: cwd}, After: edge.Endpoint{Ref: "task:B/1", CWD: cwd}},
		},
	})
	if want := []string{"A/1>A/2", "A/2>A/3", "A/3>B/1", "B/1>B/2"}; !reflect.DeepEqual(links(b.Graph), want) {
		t.Fatalf("links = %v, want %v", links(b.Graph), want)
	}
	for _, l := range b.Graph.Links {
		switch l.Before + ">" + l.After {
		case "A/3>B/1":
			if l.Kind != graph.LinkEdge || l.Edge == nil || l.Edge.ID != "e1" || l.Edge.Before.Ref != "task:A/3" || l.Edge.After.CWD != cwd || l.Edge.Note != "PR の後" {
				t.Errorf("edge の辺 = %+v", l)
			}
		default:
			if l.Kind != graph.LinkTask || l.Edge != nil {
				t.Errorf("blockedBy の辺 = %+v", l)
			}
		}
	}
	got := nodes(b.Graph)
	depth := map[string]int{"A/1": 0, "A/2": 1, "A/3": 2, "B/1": 3, "B/2": 4}
	down := map[string]int{"A/1": 4, "A/2": 3, "A/3": 2, "B/1": 1, "B/2": 0}
	for key := range depth {
		if got[key].Depth != depth[key] || got[key].Downstream != down[key] {
			t.Errorf("%s depth/downstream = %d/%d, want %d/%d", key, got[key].Depth, got[key].Downstream, depth[key], down[key])
		}
	}
}

// session: の端点は、after 側ならそのセッションの根のタスク (同じセッションの blockedBy が無いもの)、
// before 側なら末端のタスク (同じセッションのどのタスクも止めていないもの) につなぐ。同じ名前のセッションが
// 複数あれば最後に動いたもの (pane があればそれ、無ければタスクの更新が新しいもの)。タスクの無い生きている
// セッションはそのノードへ。まだ起きていないセッションと issue: / pr: は参照を鍵にした置き場のノード (pending)、
// 終了していてタスクを作らなかったセッションは置き場のノード (exited) にする。
func TestGraph_refEndpoints(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	old := task("O", "1", "古い同名", "completed", cwd)
	old.UpdatedAt = t0.Add(-time.Hour)
	b := build(input{
		Domain: domain.Work,
		PID:    42,
		Snapshot: snapshot(
			state.PaneStatus{PaneID: "p1", SessionID: "L", CWD: cwd, AgentStatus: "working", AgentMessage: "起きたばかり"},
		),
		Tasks: []tasklog.Task{
			task("A", "1", "根", "pending", cwd),
			task("A", "2", "中", "pending", cwd, "1"),
			task("A", "3", "末端 1", "pending", cwd, "2"),
			task("A", "4", "末端 2", "pending", cwd, "1"),
			old,
			task("N", "1", "新しい同名", "pending", cwd),
		},
		Edges: []edge.Edge{
			e("e1", "issue:o/r#1", "session:alpha"),
			e("e2", "session:alpha", "pr:o/r#2"),
			e("e3", "session:dup", "session:live"),
			e("e4", "session:later", "session:gone"),
		},
		SessionNames: map[string]string{"A": "alpha", "O": "dup", "N": "dup", "L": "live", "G": "gone"},
	})
	// 辺は before、after の Key の順に並ぶ。
	want := []string{
		"A/1>A/2", "A/1>A/4", "A/2>A/3",
		"A/3>ref:pr:o/r#2", "A/4>ref:pr:o/r#2",
		"N/1>L/session",
		"ref:issue:o/r#1>A/1",
		"ref:session:later>ref:session:gone",
	}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
	got := nodes(b.Graph)
	place := map[string]string{
		"ref:issue:o/r#1":   graph.StatePending,
		"ref:pr:o/r#2":      graph.StatePending,
		"ref:session:later": graph.StatePending,
		"ref:session:gone":  graph.StateExited,
	}
	for key, state := range place {
		n := got[key]
		if n.Kind != graph.KindRef || n.State != state || n.Ref != key[len("ref:"):] || n.CWD != cwd {
			t.Errorf("%s = %+v, want 置き場のノード (%s)", key, n, state)
		}
	}
	if got["ref:session:later"].Title != "later" || got["ref:issue:o/r#1"].Title != "issue:o/r#1" {
		t.Errorf("title = %q / %q", got["ref:session:later"].Title, got["ref:issue:o/r#1"].Title)
	}
	if got["ref:session:gone"].SessionID != "G" || got["ref:session:gone"].Session != "gone" {
		t.Errorf("終了したセッションの置き場 = %+v", got["ref:session:gone"])
	}
	// 名前のあるセッションのタスクのノードには、session: の参照ではなく task: の参照を持たせる。
	if got["L/session"].Ref != "session:live" {
		t.Errorf("L/session の参照 = %q", got["L/session"].Ref)
	}
	// 同名のセッションは表示名 (Session) に session ID の先頭が付くが、SessionName は SendMessage の宛先に書く名前のまま。
	if n := got["N/1"]; n.Session != "dup N" || n.SessionName != "dup" {
		t.Errorf("同名のセッションの名前 = %q / %q", n.Session, n.SessionName)
	}
	if n := got["ref:session:gone"]; n.SessionName != "gone" {
		t.Errorf("終了したセッションの置き場の名前 = %q", n.SessionName)
	}
	for _, l := range b.Graph.Links {
		if l.Before == "N/1" && (l.Edge == nil || l.Edge.Before.Ref != "session:dup") {
			t.Errorf("辺の元の参照 = %+v", l.Edge)
		}
	}
}

// 名前に当たるセッションがほかの domain の側にしか無ければ、まだ起きていないものとして扱い、その session ID を
// 載せない (私用と業務を混ぜない)。セッションのタスクがすべて輪になっていて根も末端も無ければ、タスク全部につなぐ。
func TestGraph_refEndpointsEdgeCases(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	b := build(input{
		Domain: domain.Work,
		Tasks: []tasklog.Task{
			task("C", "1", "x", "pending", cwd, "2"),
			task("C", "2", "y", "pending", cwd, "1"),
			task("D", "1", "後", "pending", cwd),
		},
		Edges: []edge.Edge{
			e("e1", "session:home-only", "task:D/1"),
			e("e2", "session:loop", "task:D/1"),
		},
		Elsewhere:    func(id string) bool { return id == "H" },
		SessionNames: map[string]string{"H": "home-only", "C": "loop", "D": "after"},
	})
	got := nodes(b.Graph)
	if n := got["ref:session:home-only"]; n.State != graph.StatePending || n.SessionID != "" || n.Session != "" {
		t.Errorf("ほかの domain のセッション = %+v, want session ID の無い pending", n)
	}
	want := []string{"C/1>C/2", "C/1>D/1", "C/2>C/1", "C/2>D/1", "ref:session:home-only>D/1"}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
}

// 止まっているノードから下流の最長の鎖 (longest chain) と、ノードごとに上流をたどった原因の鎖 (blocked by)。
// 原因の鎖は完了していない上流だけをたどり、止まっているもの、進行中のものの順に選ぶ。
func TestGraph_chains(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		Domain: domain.Work,
		PID:    42,
		Snapshot: snapshot(
			state.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "blocked", AgentKind: "question"},
			state.PaneStatus{PaneID: "p2", SessionID: "B", CWD: cwd, AgentStatus: "working"},
			state.PaneStatus{PaneID: "p3", SessionID: "C", CWD: cwd, AgentStatus: "blocked", AgentKind: "permission"},
		),
		Tasks: []tasklog.Task{
			task("A", "1", "論点を決める", "in_progress", cwd),
			task("A", "2", "段5", "pending", cwd, "1", "0"),
			task("A", "0", "済んだ前提", "completed", cwd),
			task("A", "3", "リリース", "pending", cwd, "2"),
			task("A", "4", "確認", "pending", cwd, "3"),
			task("B", "1", "別の作業", "in_progress", cwd),
			task("C", "1", "止まった小さな作業", "in_progress", cwd),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "task:B/1", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/2", CWD: cwd}},
		},
	})
	if want := []string{"A/1", "A/2", "A/3", "A/4"}; !reflect.DeepEqual(b.Graph.LongestChain, want) {
		t.Errorf("longest chain = %v, want %v", b.Graph.LongestChain, want)
	}
	got := nodes(b.Graph)
	if want := []string{"A/3", "A/2", "A/1"}; !reflect.DeepEqual(got["A/4"].BlockedBy, want) {
		t.Errorf("A/4 blocked by = %v, want %v", got["A/4"].BlockedBy, want)
	}
	if len(got["A/1"].BlockedBy) != 0 {
		t.Errorf("A/1 blocked by = %v, want 空", got["A/1"].BlockedBy)
	}
	// ノードは下流の多い順 (同じなら Key の順) に並ぶ。mod はこの順で根を描く。
	var order []string
	for _, n := range b.Graph.Nodes {
		order = append(order, n.Key)
	}
	if want := []string{"A/0", "A/1", "B/1", "A/2", "A/3", "A/4", "C/1"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// drawRows は rows を目で読める形にする (internal/board/dag のテストと同じ形)。
func drawRows(rows []dag.Row) string {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Node == "" {
			lines = append(lines, r.Lanes)
			continue
		}
		lines = append(lines, r.Lanes+" "+r.Node)
	}
	return strings.Join(lines, "\n")
}

// rows は Nodes の順を order にしたレーンの行。完了したノードとその辺を落としたものと、全部を含むものの 2 組。
// edge の辺 (セッションをまたぐ) は破線。
func TestGraph_rows(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		Domain: domain.Work,
		PID:    42,
		Snapshot: snapshot(
			state.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "blocked", AgentKind: "question"},
			state.PaneStatus{PaneID: "p2", SessionID: "B", CWD: cwd, AgentStatus: "working"},
			state.PaneStatus{PaneID: "p3", SessionID: "C", CWD: cwd, AgentStatus: "blocked", AgentKind: "permission"},
		),
		Tasks: []tasklog.Task{
			task("A", "1", "論点を決める", "in_progress", cwd),
			task("A", "2", "段5", "pending", cwd, "1", "0"),
			task("A", "0", "済んだ前提", "completed", cwd),
			task("A", "3", "リリース", "pending", cwd, "2"),
			task("A", "4", "確認", "pending", cwd, "3"),
			task("B", "1", "別の作業", "in_progress", cwd),
			task("C", "1", "止まった小さな作業", "in_progress", cwd),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "task:B/1", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/2", CWD: cwd}},
		},
	})
	if got, want := drawRows(b.Graph.Rows), "* A/1\n│ * B/1\n├┄┘\n* A/2\n* A/3\n* A/4\n* C/1"; got != want {
		t.Errorf("rows =\n%s\nwant\n%s", got, want)
	}
	if got, want := drawRows(b.Graph.RowsWithCompleted), "* A/0\n│ * A/1\n│ │ * B/1\n├─┴┄┘\n* A/2\n* A/3\n* A/4\n* C/1"; got != want {
		t.Errorf("rows_with_completed =\n%s\nwant\n%s", got, want)
	}
	// longest chain (A/1 → A/2 → A/3 → A/4) の辺の罫線に印が付く。A/1 → A/2 は B/1 の行を縦線で通り抜け、
	// 合流の行で ├ に入る。B/1 → A/2 の横線には付かない。
	marks := func(rows []dag.Row) map[int][]int {
		out := map[int][]int{}
		for i, r := range rows {
			if len(r.Marks) > 0 {
				out[i] = r.Marks
			}
		}
		return out
	}
	if got, want := marks(b.Graph.Rows), map[int][]int{1: {0}, 2: {0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows の印 = %v, want %v", got, want)
	}
	if got, want := marks(b.Graph.RowsWithCompleted), map[int][]int{2: {2}, 3: {0, 1, 2}}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows_with_completed の印 = %v, want %v", got, want)
	}
	// ノードが無ければ空の配列 (JSON で null にしない)。
	empty := build(input{Domain: domain.Work})
	if empty.Graph.Rows == nil || empty.Graph.RowsWithCompleted == nil {
		t.Errorf("rows = %v / %v, want 空の配列", empty.Graph.Rows, empty.Graph.RowsWithCompleted)
	}
}

// 止まっているノードも待ちの輪も無ければ longest chain は空。循環した辺があっても止まらずに組む。
func TestGraph_noBlockedAndCycle(t *testing.T) {
	const cwd = "/w/a"
	calm := build(input{
		Domain: domain.Work,
		Tasks:  []tasklog.Task{task("A", "1", "x", "pending", cwd), task("A", "2", "y", "pending", cwd, "1")},
	})
	if len(calm.Graph.LongestChain) != 0 {
		t.Errorf("longest chain = %v, want 空", calm.Graph.LongestChain)
	}
	b := build(input{
		Domain: domain.Work,
		Tasks: []tasklog.Task{
			task("A", "1", "x", "pending", cwd, "2"),
			task("A", "2", "y", "pending", cwd, "1"),
		},
	})
	if len(b.Graph.Nodes) != 2 || len(b.Graph.Links) != 2 {
		t.Errorf("graph = %+v", b.Graph)
	}
}

// 待ちが輪になったノード (デッドロック) には印を付ける。輪の下流にいるだけのノードには付けない。
func TestGraph_deadlock(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		Domain: domain.Work,
		Tasks: []tasklog.Task{
			task("A", "1", "x", "pending", cwd, "2"),
			task("A", "2", "y", "pending", cwd),
			task("A", "3", "下流", "pending", cwd, "1"),
			task("A", "4", "無関係", "pending", cwd),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "task:A/1", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/2", CWD: cwd}},
		},
	})
	got := nodes(b.Graph)
	if !got["A/1"].Deadlock || !got["A/2"].Deadlock || got["A/3"].Deadlock || got["A/4"].Deadlock {
		t.Errorf("deadlock = %v / %v / %v / %v, want true / true / false / false", got["A/1"].Deadlock, got["A/2"].Deadlock, got["A/3"].Deadlock, got["A/4"].Deadlock)
	}
	// 待ちの輪も止まっているノードとして longest chain の起点にする (B と A は輪を ■ に数える)。
	if want := []string{"A/1", "A/2"}; !reflect.DeepEqual(b.Graph.LongestChain, want) {
		t.Errorf("longest chain = %v, want %v", b.Graph.LongestChain, want)
	}
}

// ノードの parked は、そのタスクの途中で置かれた open のメモの数。タスクの無いセッションのノードには、
// タスクを指さずにそのセッションが置いたメモを数える。メモの source は task_id を JSON に出す。
func TestGraph_parked(t *testing.T) {
	const cwd = "/w/a"
	memo := func(id string, sources ...parking.Source) parking.Item {
		return parking.Item{ID: id, Domain: domain.Work, Repo: cwd, Status: parking.StatusOpen, Entry: parking.Entry{Kind: parking.KindAside, Title: id}, Sources: sources}
	}
	b := build(input{
		Domain:   domain.Work,
		PID:      42,
		Snapshot: snapshot(state.PaneStatus{PaneID: "p1", SessionID: "S", CWD: cwd, AgentStatus: "working"}),
		Tasks:    []tasklog.Task{task("A", "1", "x", "in_progress", cwd), task("A", "2", "y", "pending", cwd)},
		Parking: []parking.Item{
			memo("m1", parking.Source{SessionID: "A", TaskID: "1", CWD: cwd, At: t0}),
			memo("m2", parking.Source{SessionID: "A", TaskID: "1", CWD: cwd, At: t0}, parking.Source{SessionID: "S", CWD: cwd, At: t0}),
			memo("m3", parking.Source{SessionID: "A", CWD: cwd, At: t0}),
		},
	})
	got := nodes(b.Graph)
	if got["A/1"].Parked != 2 || got["A/2"].Parked != 0 || got["S/session"].Parked != 1 {
		t.Errorf("parked = %d / %d / %d, want 2 / 0 / 1", got["A/1"].Parked, got["A/2"].Parked, got["S/session"].Parked)
	}
	if b.Rows[0].Parking[0].Sources[0].TaskID != "1" {
		t.Errorf("source の task_id = %+v", b.Rows[0].Parking[0].Sources)
	}
}

// タスクの description に書かれたメモの id をメモと照合し、ノードの memos に書かれた順で付ける。open 以外のメモも
// 照合するが、パーキングの列と parked には open のメモだけを出す。記録に無い id (消えたメモ) は付けない。
func TestGraph_memos(t *testing.T) {
	const cwd = "/w/a"
	const handed, open, discarded, gone = "20261025T010000-0000000a", "20261025T020000-0000000b", "20261025T030000-0000000c", "20261025T040000-0000000d"
	memo := func(id, title, status string) parking.Item {
		return parking.Item{ID: id, Domain: domain.Work, Repo: cwd, Status: status, Entry: parking.Entry{Kind: parking.KindAside, Title: title},
			Sources: []parking.Source{{SessionID: "A", TaskID: "1", CWD: cwd, At: t0}}}
	}
	withDescription := func(t tasklog.Task, description string) tasklog.Task {
		t.Description = description
		return t
	}
	b := build(input{
		Domain:   domain.Work,
		PID:      42,
		Snapshot: snapshot(state.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"}),
		Tasks: []tasklog.Task{
			withDescription(task("A", "1", "x", "in_progress", cwd), "メモ "+open+" と "+handed+" と "+gone+" を扱う。(メモ "+handed+")"),
			withDescription(task("A", "2", "y", "pending", cwd), "メモ "+discarded),
			task("A", "3", "z", "pending", cwd),
		},
		Parking: []parking.Item{
			memo(handed, "渡したメモ", parking.StatusHanded),
			memo(open, "まだ置いてあるメモ", parking.StatusOpen),
			memo(discarded, "捨てたメモ", parking.StatusDiscarded),
		},
	})
	got := nodes(b.Graph)
	want := []graph.MemoRef{
		{ID: open, Title: "まだ置いてあるメモ", Status: parking.StatusOpen},
		{ID: handed, Title: "渡したメモ", Status: parking.StatusHanded},
	}
	if !reflect.DeepEqual(got["A/1"].Memos, want) {
		t.Errorf("A/1 memos = %+v, want %+v", got["A/1"].Memos, want)
	}
	if want := []graph.MemoRef{{ID: discarded, Title: "捨てたメモ", Status: parking.StatusDiscarded}}; !reflect.DeepEqual(got["A/2"].Memos, want) {
		t.Errorf("A/2 memos = %+v, want %+v", got["A/2"].Memos, want)
	}
	if got["A/3"].Memos == nil || len(got["A/3"].Memos) != 0 {
		t.Errorf("description の無いタスクの memos は空の配列: %#v", got["A/3"].Memos)
	}
	if got["A/1"].Parked != 1 {
		t.Errorf("parked = %d, want 1 (open のメモだけ数える)", got["A/1"].Parked)
	}
	if len(b.Rows) != 1 || len(b.Rows[0].Parking) != 1 || b.Rows[0].Parking[0].ID != open {
		t.Errorf("パーキングの列は open のメモだけ: %+v", b.Rows)
	}
}
