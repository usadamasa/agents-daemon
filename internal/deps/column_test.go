package deps

import (
	"reflect"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// 段は手当てが要るもの、作業中、返事待ち、未着手、外部の参照、完了、終了したセッションに残ったものの順。
// 終了したセッションは最後に動いた時刻が新しくても外部の参照より下に出す (#446)。同じ段は最後に動いた時刻の新しい順で、時刻の無いノードはその後ろ。それも同じなら repo、セッション名、key の順。
func TestColumnOrder(t *testing.T) {
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	node := func(key, state, cwd, session string) protocol.GraphNode {
		return protocol.GraphNode{Key: key, State: state, CWD: cwd, Session: session}
	}
	nodes := []protocol.GraphNode{
		node("done", "completed", "/a", "s"),
		node("ext", "external", "", ""),
		node("wait", "waiting", "", ""),
		node("gone", "exited", "/a", "s"),
		node("todo-old", "pending", "/a", "s"),
		node("todo-new", "pending", "/a", "s"),
		node("idle", "done", "/a", "s"),
		node("unknown", "unknown", "/a", "s"),
		node("work-b", "working", "/b", "s"),
		node("work-a-y", "working", "/a", "y"),
		node("work-a-x", "working", "/a", "x"),
		node("work-timed", "working", "/b", "z"),
		node("limit", "limit", "/a", "s"),
		node("ask", "question", "/a", "s"),
		node("perm", "permission", "/a", "s"),
		node("err", "error", "/a", "s"),
	}
	active := map[string]time.Time{
		"todo-old": at(1), "todo-new": at(5),
		"work-timed": at(3),
		"limit":      at(1), "ask": at(4), "perm": at(2), "err": at(3),
		"idle": at(1), "unknown": at(2),
		"gone": at(9),
	}
	repo := func(cwd string) string { return cwd }

	got := columnOrder(nodes, active, repo)
	want := []string{
		"ask", "err", "perm", "limit",
		"work-timed", "work-a-x", "work-a-y", "work-b",
		"unknown", "idle",
		"todo-new", "todo-old",
		"ext", "wait",
		"done",
		"gone",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("columnOrder =\n  %v\nwant\n  %v", got, want)
	}
}

// 枠 (rex の session、rex の外の生きているセッション) は中で一番先に来るノードの位置に並べ、枠の中は window (rex の
// 外は Claude のセッション) ごとに、最初に来るノードの順にまとめる。まとめないノード (終了したもの、参照) は
// 互いの順を保つ (#633)。
func TestGroupByRex(t *testing.T) {
	rex := func(session, window string) *protocol.RexPlacement {
		return &protocol.RexPlacement{SessionID: session, SessionLabel: session, WindowID: window, WindowLabel: window}
	}
	nodes := []protocol.GraphNode{
		{Key: "a1", Live: true, SessionID: "A1", Rex: rex("S1", "wA")},
		{Key: "x", State: "exited", SessionID: "X"},
		{Key: "b1", Live: true, SessionID: "B1", Rex: rex("S2", "wC")},
		{Key: "o1", Live: true, SessionID: "P"},
		{Key: "a2", Live: true, SessionID: "A2", Rex: rex("S1", "wB")},
		{Key: "o2", Live: true, SessionID: "Q"},
		{Key: "a3", Live: true, SessionID: "A1", Rex: rex("S1", "wA")},
		{Key: "o3", Live: true, SessionID: "P"},
		{Key: "y", State: "completed", SessionID: "Y"},
		{Key: "pr:1", Kind: "ref"},
	}
	order := make([]string, len(nodes))
	for i, n := range nodes {
		order[i] = n.Key
	}
	got := groupByRex(order, nodes)
	want := []string{"a1", "a3", "a2", "x", "b1", "o1", "o3", "o2", "y", "pr:1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groupByRex =\n  %v\nwant\n  %v", got, want)
	}
}

// 生きているセッションのノードには、status.json の pane の rex の置き場を写す。終了したセッションのノードには付けない。
// column_order は rex の session ごとにまとまる。
func TestBuild_rexPlacement(t *testing.T) {
	const cwd = "/w/a"
	s1 := &protocol.RexPlacement{SessionID: "session:1", SessionLabel: "work", WindowID: "window:1", WindowLabel: "peer-a"}
	s2 := &protocol.RexPlacement{SessionID: "session:2", SessionLabel: "other", WindowID: "window:2", WindowLabel: "peer-b"}
	a := task("A", "1", "A の作業", "in_progress", cwd)
	a.UpdatedAt = t0.Add(3 * time.Minute)
	b := task("B", "1", "B の作業", "in_progress", cwd)
	b.UpdatedAt = t0.Add(2 * time.Minute)
	c := task("C", "1", "C の作業", "in_progress", cwd)
	c.UpdatedAt = t0.Add(1 * time.Minute)
	gone := task("G", "1", "終わった", "in_progress", cwd)
	d := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working", Rex: s1},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "B", CWD: cwd, AgentStatus: "working", Rex: s2},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "C", CWD: cwd, AgentStatus: "working", Rex: s1},
		),
		Tasks: []record.Task{a, b, c, gone},
	})
	got := map[string]*protocol.RexPlacement{}
	for _, n := range d.Graph.Nodes {
		got[n.Key] = n.Rex
	}
	for key, want := range map[string]*protocol.RexPlacement{"task:A/1": s1, "task:B/1": s2, "task:C/1": s1, "task:G/1": nil} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s の rex = %+v, want %+v", key, got[key], want)
		}
	}
	if want := []string{"task:A/1", "task:C/1", "task:B/1", "task:G/1"}; !reflect.DeepEqual(d.Graph.ColumnOrder, want) {
		t.Errorf("column_order = %v, want %v (rex の session ごとにまとまる)", d.Graph.ColumnOrder, want)
	}
}

// build は全ノードの key を column_order に載せ、タスクの更新時刻で並べる。repo は main worktree で揃える。
func TestBuild_columnOrder(t *testing.T) {
	const main, wt = "/src/home/agents-daemon", "/src/home/agents-daemon/.claude/worktrees/x"
	older := task("A", "1", "古い", "pending", wt)
	newer := task("A", "2", "新しい", "pending", main)
	newer.UpdatedAt = t0.Add(time.Hour)
	d := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: main, AgentStatus: "working"}),
		Tasks:    []record.Task{older, newer},
		Locate: places(map[string]gitrepo.Place{
			main: {Repo: main, Branch: "main"},
			wt:   {Repo: main, Branch: "x"},
		}),
	})
	if len(d.Graph.ColumnOrder) != len(d.Graph.Nodes) {
		t.Fatalf("column_order = %v, nodes = %d", d.Graph.ColumnOrder, len(d.Graph.Nodes))
	}
	if want := []string{"task:A/2", "task:A/1"}; !reflect.DeepEqual(d.Graph.ColumnOrder, want) {
		t.Errorf("column_order = %v, want %v", d.Graph.ColumnOrder, want)
	}
}
