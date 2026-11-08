package deps

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/deps/graph/dag"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
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
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "W", CWD: "/w/a", AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "D", CWD: "/w/a", AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "Q", CWD: "/w/a", AgentStatus: "blocked", AgentKind: "question"},
			daemonstate.PaneStatus{PaneID: "p4", SessionID: "P", CWD: "/w/a", AgentStatus: "blocked", AgentKind: "permission"},
			daemonstate.PaneStatus{PaneID: "p5", SessionID: "E", CWD: "/w/a", AgentStatus: "error"},
			daemonstate.PaneStatus{PaneID: "p6", SessionID: "L", CWD: "/w/a", AgentStatus: "error", MonitorStatus: "waiting", WaitUntil: &until},
			daemonstate.PaneStatus{PaneID: "p7", SessionID: "S", CWD: "/w/s", AgentStatus: "working", AgentMessage: "調べている"},
			daemonstate.PaneStatus{PaneID: "p8", SessionID: "T", CWD: "/w/t", AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p9", SessionID: "U", CWD: "/w/u", AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p10", SessionID: "V", CWD: "/w/dotfiles", AgentStatus: "unknown"},
			daemonstate.PaneStatus{PaneID: "p11", SessionID: "N", CWD: "/w/a", AgentStatus: "unknown"},
		),
		Tasks: []record.Task{
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
		Todos: []record.TodoList{{SessionID: "T", CWD: "/w/t", UpdatedAt: t0, Todos: []record.Todo{
			{Content: "a", Status: "completed"}, {Content: "b", Status: "in_progress"},
		}}},
		SessionNames: map[string]string{"W": "writer", "U": "waiter"},
	})
	got := nodes(b.Graph)
	want := map[string]string{
		"task:W/1": graph.StateWorking, "task:W/2": graph.StatePending, "task:W/3": graph.StateCompleted,
		"task:D/1": graph.StateDone, "task:Q/1": graph.StateQuestion, "task:P/1": graph.StatePermission, "task:E/1": graph.StateError, "task:L/1": graph.StateLimit,
		"task:X/1": graph.StateExited, "task:X/2": graph.StateExited,
		"sid:S": graph.StateWorking, "sid:U": graph.StateDone,
		// 状態を分類できないセッション (申告の前、古い record) は working に倒さない。
		"sid:V": graph.StateUnknown, "task:N/1": graph.StateUnknown,
		"todo:T/0": graph.StateCompleted, "todo:T/1": graph.StateWorking,
	}
	if len(got) != len(want) {
		t.Errorf("nodes = %d, want %d: %+v", len(got), len(want), got)
	}
	for key, state := range want {
		if got[key].State != state {
			t.Errorf("%s state = %q, want %q", key, got[key].State, state)
		}
	}
	w := got["task:W/1"]
	if w.Kind != graph.KindTask || w.Title != "書く" || !w.Endpoint || w.TaskID != "1" || w.CWD != "/w/a" || w.SessionID != "W" || w.Session != "writer" || !w.Live {
		t.Errorf("task:W/1 = %+v", w)
	}
	if got["task:L/1"].Detail != "14:30" || got["task:Q/1"].Detail != "" {
		t.Errorf("detail = %q / %q, want 上限の解除時刻だけ", got["task:L/1"].Detail, got["task:Q/1"].Detail)
	}
	// タスクの無いセッションは名前の有無によらず sid: で辺の端点にできる。題はセッション名 (無ければ cwd の末尾) で、
	// Claude Code が申告した今の作業 (msg) は summary に回す (#394)。
	if s := got["sid:S"]; s.Kind != graph.KindSession || s.Title != "s" || s.Summary != "調べている" || !s.Endpoint || s.TaskID != "" {
		t.Errorf("sid:S = %+v", s)
	}
	if u := got["sid:U"]; u.Title != "waiter" || u.Summary != "" {
		t.Errorf("sid:U title / summary = %q / %q, want waiter / 空", u.Title, u.Summary)
	}
	if v := got["sid:V"]; v.Title != "dotfiles" {
		t.Errorf("sid:V title = %q, want dotfiles", v.Title)
	}
	if got["todo:T/1"].Endpoint {
		t.Errorf("todo は辺の端点にできない")
	}
	if got["task:X/1"].Live {
		t.Errorf("task:X/1 は終了したセッション")
	}
}

// タスクのあるセッションの今の作業 (msg) は、進行中のタスクと TodoWrite の項目の summary に添える (#157)。
// 未着手と完了の作業はセッションの今の作業と関係が無いので添えない。
func TestGraph_taskSummary(t *testing.T) {
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "W", CWD: "/w/a", AgentStatus: "working", AgentMessage: "テストを直している"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "T", CWD: "/w/t", AgentStatus: "done", AgentMessage: "済ませた"},
		),
		Tasks: []record.Task{
			task("W", "1", "書く", "in_progress", "/w/a"),
			task("W", "2", "次", "pending", "/w/a"),
			task("W", "3", "済んだ", "completed", "/w/a"),
			task("X", "1", "残った進行中", "in_progress", "/w/x"),
		},
		Todos: []record.TodoList{{SessionID: "T", CWD: "/w/t", UpdatedAt: t0, Todos: []record.Todo{
			{Content: "a", Status: "completed"}, {Content: "b", Status: "in_progress"},
		}}},
	})
	got := nodes(b.Graph)
	want := map[string]string{
		"task:W/1": "テストを直している", "task:W/2": "", "task:W/3": "",
		"todo:T/0": "", "todo:T/1": "済ませた",
		// 終了したセッションには申告が無い。
		"task:X/1": "",
	}
	for key, summary := range want {
		if got[key].Summary != summary {
			t.Errorf("%s summary = %q, want %q", key, got[key].Summary, summary)
		}
	}
}

// 同じセッションの blockedBy と、task: の参照の辺 (edge list) を 1 つの DAG にする。
// 記録に無いタスクを指す辺は描かない。下流の数と段 (前提の深さ) を数える。
func TestGraph_linksDepthAndDownstream(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		Tasks: []record.Task{
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
	if want := []string{"task:A/1>task:A/2", "task:A/2>task:A/3", "task:A/3>task:B/1", "task:B/1>task:B/2"}; !reflect.DeepEqual(links(b.Graph), want) {
		t.Fatalf("links = %v, want %v", links(b.Graph), want)
	}
	for _, l := range b.Graph.Links {
		switch l.Before + ">" + l.After {
		case "task:A/3>task:B/1":
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
	depth := map[string]int{"task:A/1": 0, "task:A/2": 1, "task:A/3": 2, "task:B/1": 3, "task:B/2": 4}
	down := map[string]int{"task:A/1": 4, "task:A/2": 3, "task:A/3": 2, "task:B/1": 1, "task:B/2": 0}
	for key := range depth {
		if got[key].Depth != depth[key] || got[key].Downstream != down[key] {
			t.Errorf("%s depth/downstream = %d/%d, want %d/%d", key, got[key].Depth, got[key].Downstream, depth[key], down[key])
		}
	}
}

// sid: の端点は、after 側ならそのセッションの根のタスク (同じセッションの blockedBy が無いもの)、
// before 側なら末端のタスク (同じセッションのどのタスクも止めていないもの。複数なら sid: のノードに集める) に
// つなぐ。タスクの無い生きている
// セッションはそのノードへ。daemon がまだ結びつけていない session:<名前> は、同じ名前のセッションが生きていても
// 名前を key にした置き場のノード (pending) にする。issue: / pr: は参照を key にした置き場のノード (external)、
// 終了していてタスクを作らなかったセッションは sid: を key にした置き場のノード (exited) にする。
func TestGraph_refEndpoints(t *testing.T) {
	const cwd = "/w/a"
	ep := func(r string) edge.Endpoint {
		if name, ok := strings.CutPrefix(r, "="); ok {
			// =<sid>@<名前> は daemon が session:<名前> から結びつけた端点。
			id, from, _ := strings.Cut(name, "@")
			return edge.Endpoint{Ref: "sid:" + id, CWD: cwd, BoundFrom: "session:" + from}
		}
		return edge.Endpoint{Ref: r, CWD: cwd}
	}
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: ep(before), After: ep(after)}
	}
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "L", CWD: cwd, AgentStatus: "working", AgentMessage: "起きたばかり"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "A", CWD: cwd, AgentStatus: "working"},
		),
		Tasks: []record.Task{
			task("A", "1", "根", "pending", cwd),
			task("A", "2", "中", "pending", cwd, "1"),
			task("A", "3", "末端 1", "pending", cwd, "2"),
			task("A", "4", "末端 2", "pending", cwd, "1"),
			task("N", "1", "同名", "pending", cwd),
			task("O", "1", "同名", "pending", cwd),
		},
		Edges: []edge.Edge{
			e("e1", "issue:o/r#1", "=A@alpha"),
			e("e2", "=A@alpha", "pr:o/r#2"),
			e("e3", "=N@dup", "=L@live"),
			e("e4", "session:later", "sid:G"),
			e("e5", "sid:G", "sid:A"),
			e("e6", "sid:Z", "sid:L"),
			e("e7", "session:alpha", "issue:o/r#3"),
		},
		SessionNames: map[string]string{"A": "alpha", "O": "dup", "N": "dup", "L": "live", "G": "gone"},
		// 台帳の名前は ListAgents の名前。名前を付けずに起動したセッションでは statusline の題と違う。
		Sessions: []ledger.Session{
			{SessionID: "A", Name: "alpha"}, {SessionID: "N", Name: "dup"}, {SessionID: "O", Name: "dup-2c"},
			{SessionID: "L", Name: "w-3d"}, {SessionID: "G", Name: "w-7f"},
		},
	})
	// 辺は before、after の Key の順に並ぶ。
	want := []string{
		"issue:o/r#1>task:A/1",
		"session:alpha>issue:o/r#3",
		"session:later>sid:G",
		"sid:A>pr:o/r#2",
		"sid:G>task:A/1",
		"sid:Z>sid:L",
		"task:A/1>task:A/2", "task:A/1>task:A/4", "task:A/2>task:A/3",
		"task:A/3>sid:A", "task:A/4>sid:A",
		"task:N/1>sid:L",
	}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
	got := nodes(b.Graph)
	place := map[string]string{
		"issue:o/r#1":   graph.StateExternal,
		"issue:o/r#3":   graph.StateExternal,
		"pr:o/r#2":      graph.StateExternal,
		"session:later": graph.StatePending,
		"session:alpha": graph.StatePending,
		"sid:G":         graph.StateExited,
		"sid:Z":         graph.StateExited,
	}
	for key, state := range place {
		n := got[key]
		if n.Kind != graph.KindRef || n.State != state || !n.Endpoint || n.CWD != cwd {
			t.Errorf("%s = %+v, want 置き場のノード (%s)", key, n, state)
		}
	}
	if got["session:later"].Title != "later" || got["session:alpha"].Title != "alpha" || got["issue:o/r#1"].Title != "issue #1 · o/r" || got["sid:Z"].Title != "sid:Z" {
		t.Errorf("title = %q / %q / %q / %q", got["session:later"].Title, got["session:alpha"].Title, got["issue:o/r#1"].Title, got["sid:Z"].Title)
	}
	// issue / PR の置き場は外部の参照 (external) で、題は番号と repo、ref_kind に種類を持つ (#393)。
	if n := got["pr:o/r#2"]; n.Title != "PR #2 · o/r" || n.RefKind != "pr" || got["issue:o/r#1"].RefKind != "issue" {
		t.Errorf("pr:o/r#2 = %+v / issue:o/r#1 ref_kind = %q", n, got["issue:o/r#1"].RefKind)
	}
	if n := got["session:later"]; n.RefKind != "" {
		t.Errorf("session: の置き場に ref_kind = %q", n.RefKind)
	}
	if n := got["session:alpha"]; n.SessionID != "" || n.Live {
		t.Errorf("結びつく前の名前の置き場はセッションを持たない: %+v", n)
	}
	// 表示名 (Session、Title) は statusline の題、SessionName は台帳の ListAgents の名前。
	if n := got["sid:G"]; n.SessionID != "G" || n.Session != "gone" || n.SessionName != "w-7f" || n.Title != "gone" {
		t.Errorf("終了したセッションの置き場 = %+v", n)
	}
	// 名前のあるタスクの無いセッションも sid: を key にする。
	if n := got["sid:L"]; n.Kind != graph.KindSession || n.Session != "live" || n.SessionName != "w-3d" {
		t.Errorf("sid:L = %+v", n)
	}
	// 同名のセッションは表示名 (Session) に session ID の先頭が付くが、SessionName は SendMessage の宛先に書く名前のまま。
	if n := got["task:N/1"]; n.Session != "dup N" || n.SessionName != "dup" {
		t.Errorf("同名のセッションの名前 = %q / %q", n.Session, n.SessionName)
	}
	if n := got["task:O/1"]; n.Session != "dup O" || n.SessionName != "dup-2c" {
		t.Errorf("同名のセッションの名前 = %q / %q", n.Session, n.SessionName)
	}
	// 辺の元の記録は結びつけた後の参照と、書いたときの参照を持つ。
	for _, l := range b.Graph.Links {
		if l.Before == "task:N/1" && (l.Edge == nil || l.Edge.Before.Ref != "sid:N" || l.Edge.Before.BoundFrom != "session:dup") {
			t.Errorf("辺の元の参照 = %+v", l.Edge)
		}
	}
}

// タスクを作らずに終了したセッションの置き場のノードは、線が 1 本でも引かれる間だけ図に載る (#455)。もう片方の端点の
// 記録が消えた辺は残っていても線にならず、置き場だけが浮くので作らない。
func TestGraph_exitedPlaceholderNeedsLink(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	b := build(input{
		Tasks: []record.Task{task("A", "1", "a", "pending", cwd)},
		Edges: []edge.Edge{
			e("drawn", "sid:G", "task:A/1"),
			e("after-gone", "sid:Z", "task:A/9"),
			e("before-gone", "task:A/9", "sid:Y"),
			// 同じ置き場に線の引かれない辺と引かれる辺があれば、線の引かれる辺で残る。
			e("mixed-gone", "sid:M", "task:A/9"),
			e("mixed-drawn", "sid:M", "task:A/1"),
		},
	})
	got := nodes(b.Graph)
	for _, key := range []string{"sid:G", "sid:M"} {
		if n := got[key]; n.Kind != graph.KindRef || n.State != graph.StateExited {
			t.Errorf("%s = %+v, want 線のある終了したセッションの置き場", key, n)
		}
	}
	for _, key := range []string{"sid:Z", "sid:Y"} {
		if n, ok := got[key]; ok {
			t.Errorf("%s = %+v, 線の無い置き場は図に載らない", key, n)
		}
	}
	for _, row := range b.Graph.ColumnOrder {
		if row == "sid:Z" || row == "sid:Y" {
			t.Errorf("column_order に線の無い置き場 %s がある", row)
		}
	}
	want := []string{"sid:G>task:A/1", "sid:M>task:A/1"}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
}

// 終了したセッションの置き場は、待っている作業がすべて済むと完了を隠した図から消える (#455)。全部の図には線と一緒に残る。
func TestGraph_exitedPlaceholderFolded(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	b := build(input{
		Tasks: []record.Task{
			task("A", "1", "a", "completed", cwd),
			task("B", "1", "b", "pending", cwd),
			task("C", "1", "c", "completed", cwd),
			task("D", "1", "d", "pending", cwd),
			task("F", "1", "f", "completed", cwd),
			task("J", "1", "j", "pending", cwd),
			task("K", "1", "k", "completed", cwd),
		},
		Works: []worklog.Record{{SessionID: "W", CWD: cwd, Subject: "終わった作業"}},
		Edges: []edge.Edge{
			e("done", "sid:G", "task:A/1"),
			e("open", "sid:H", "task:B/1"),
			e("mixed-done", "sid:M", "task:C/1"),
			e("mixed-open", "sid:M", "task:D/1"),
			// 下流が無い置き場は上流で決まる。
			e("from-done", "task:F/1", "sid:Y"),
			e("from-open", "task:J/1", "sid:Z"),
			// 作業の記録から作ったノードは置き場ではない。
			e("work", "sid:W", "task:K/1"),
		},
	})
	folded := map[string]bool{}
	for _, n := range b.Graph.Folded.Nodes {
		folded[n.Key] = true
	}
	all := nodes(b.Graph)
	for key, want := range map[string]bool{"sid:G": false, "sid:Y": false, "sid:H": true, "sid:M": true, "sid:Z": true, "sid:W": true} {
		if folded[key] != want {
			t.Errorf("完了を隠した図の %s = %v, want %v", key, folded[key], want)
		}
		if n, ok := all[key]; !ok || n.State != graph.StateExited {
			t.Errorf("全部の図の %s = %+v, want exited のノード", key, n)
		}
	}
	for _, want := range []string{"sid:G>task:A/1", "task:F/1>sid:Y"} {
		if !strings.Contains(strings.Join(links(b.Graph), " "), want) {
			t.Errorf("全部の図に線 %s が無い: %v", want, links(b.Graph))
		}
	}
}

// daemon が引いた外部の参照の状態 (refstate) が merged / closed なら、issue / PR の置き場は完了 (completed) になり、
// 完了を隠した図では畳まれる (#416)。open は待ち (waiting、#471)、まだ引いていない参照は外部 (external)。取りやめ (dropped) は
// 完了にせず、待っている下流を止めている人の手当ての要るノード (halted、stuck) にし、原因の鎖に選ぶ。
func TestGraph_refStates(t *testing.T) {
	const cwd = "/w/a"
	ep := func(r string) edge.Endpoint { return edge.Endpoint{Ref: r, CWD: cwd} }
	b := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"}),
		Tasks:    []record.Task{task("A", "1", "マージ待ち", "pending", cwd)},
		Edges: []edge.Edge{
			{ID: "e1", Before: ep("pr:o/r#1"), After: ep("task:A/1")},
			{ID: "e2", Before: ep("issue:o/r#2"), After: ep("task:A/1"), Note: "書いた note"},
			{ID: "e3", Before: ep("pr:o/r#3"), After: ep("task:A/1")},
			{ID: "e4", Before: ep("issue:o/r#4"), After: ep("task:A/1")},
			{ID: "e5", Before: ep("pr:o/r#5"), After: ep("task:A/1")},
		},
		RefStates: map[string]string{"pr:o/r#1": "merged", "issue:o/r#2": "closed", "pr:o/r#3": "open", "pr:o/r#5": "dropped"},
		RefTitles: map[string]string{"pr:o/r#1": "依存図に題を出す", "issue:o/r#2": "issue の題", "pr:o/r#3": "マージを待つ PR"},
	})
	got := nodes(b.Graph)
	want := map[string]string{
		"pr:o/r#1":    graph.StateCompleted,
		"issue:o/r#2": graph.StateCompleted,
		"pr:o/r#3":    graph.StateWaiting,
		"issue:o/r#4": graph.StateExternal,
		"pr:o/r#5":    graph.StateDropped,
	}
	for key, state := range want {
		if n := got[key]; n.State != state || n.Kind != graph.KindRef || n.RefKind == "" {
			t.Errorf("%s = %+v, want %s", key, n, state)
		}
	}
	// 種類は状態にかかわらず参照から付ける。題は daemon が引いた題、引けていなければ番号と repo (#472)。
	// 番号と repo は ref_label に残す。
	if n := got["pr:o/r#1"]; n.Title != "依存図に題を出す" || n.RefKind != "pr" || n.RefLabel != "PR #1 · o/r" {
		t.Errorf("pr:o/r#1 = %+v", n)
	}
	// open と引けた参照 (waiting) も題を出す。
	if n := got["pr:o/r#3"]; n.Title != "マージを待つ PR" || n.RefLabel != "PR #3 · o/r" {
		t.Errorf("waiting の pr:o/r#3 = %+v", n)
	}
	if n := got["issue:o/r#4"]; n.Title != "issue #4 · o/r" || n.RefLabel != "issue #4 · o/r" {
		t.Errorf("題を引いていない issue:o/r#4 = %+v", n)
	}
	// note の無い辺は before の参照の題を note に出す。書いた note と、題を引いていない参照の辺はそのまま。
	notes := map[string]string{}
	for _, l := range b.Graph.Links {
		if l.Edge != nil {
			notes[l.Edge.ID] = l.Edge.Note
		}
	}
	if want := map[string]string{"e1": "依存図に題を出す", "e2": "書いた note", "e3": "マージを待つ PR", "e4": "", "e5": ""}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %v, want %v", notes, want)
	}
	shown := map[string]bool{}
	for _, n := range b.Graph.Folded.Nodes {
		shown[n.Key] = true
	}
	if shown["pr:o/r#1"] || shown["issue:o/r#2"] || !shown["pr:o/r#3"] || !shown["issue:o/r#4"] || !shown["pr:o/r#5"] || !shown["task:A/1"] {
		t.Errorf("完了を隠した図のノード = %v", shown)
	}
	if n := got["pr:o/r#5"]; !n.Halted || !n.Stuck {
		t.Errorf("取りやめの参照 = %+v, want halted / stuck", n)
	}
	if n := got["task:A/1"]; !reflect.DeepEqual(n.BlockedBy, []string{"pr:o/r#5"}) {
		t.Errorf("task:A/1 の原因 = %v, want 取りやめの参照", n.BlockedBy)
	}
	if !reflect.DeepEqual(b.Graph.LongestChain, []string{"pr:o/r#5", "task:A/1"}) {
		t.Errorf("longest chain = %v", b.Graph.LongestChain)
	}
}

// セッションのタスクがすべて輪になっていて根も末端も無ければ、タスク全部につなぐ (before 側は sid: のノードに集める)。
func TestGraph_refEndpointsEdgeCases(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	b := build(input{
		Tasks: []record.Task{
			task("C", "1", "x", "pending", cwd, "2"),
			task("C", "2", "y", "pending", cwd, "1"),
			task("D", "1", "後", "pending", cwd),
		},
		Edges: []edge.Edge{
			e("e2", "sid:C", "task:D/1"),
		},
		SessionNames: map[string]string{"C": "loop", "D": "after"},
	})
	want := []string{"sid:C>task:D/1", "task:C/1>sid:C", "task:C/1>task:C/2", "task:C/2>sid:C", "task:C/2>task:C/1"}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
}

// 止まっているノードから下流の最長の鎖 (longest chain) と、ノードごとに上流をたどった原因の鎖 (blocked by)。
// 原因の鎖は完了していない上流だけをたどり、止まっているもの、進行中のものの順に選ぶ。
func TestGraph_chains(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "blocked", AgentKind: "question"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "B", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "C", CWD: cwd, AgentStatus: "blocked", AgentKind: "permission"},
		),
		Tasks: []record.Task{
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
	if want := []string{"task:A/1", "task:A/2", "task:A/3", "task:A/4"}; !reflect.DeepEqual(b.Graph.LongestChain, want) {
		t.Errorf("longest chain = %v, want %v", b.Graph.LongestChain, want)
	}
	got := nodes(b.Graph)
	if want := []string{"task:A/3", "task:A/2", "task:A/1"}; !reflect.DeepEqual(got["task:A/4"].BlockedBy, want) {
		t.Errorf("task:A/4 blocked by = %v, want %v", got["task:A/4"].BlockedBy, want)
	}
	if len(got["task:A/1"].BlockedBy) != 0 {
		t.Errorf("task:A/1 blocked by = %v, want 空", got["task:A/1"].BlockedBy)
	}
	// 止まっているかはバイナリが決めて渡す。
	for key, want := range map[string]bool{"task:A/1": true, "task:C/1": true, "task:B/1": false, "task:A/2": false} {
		if n := got[key]; n.Stuck != want || n.Halted != want {
			t.Errorf("%s halted/stuck = %v/%v, want %v", key, n.Halted, n.Stuck, want)
		}
	}
	// ノードは下流の多い順 (同じなら Key の順) に並ぶ。mod はこの順で根を描く。
	var order []string
	for _, n := range b.Graph.Nodes {
		order = append(order, n.Key)
	}
	if want := []string{"task:A/0", "task:A/1", "task:B/1", "task:A/2", "task:A/3", "task:A/4", "task:C/1"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// drawRows は rows を目で読める形にする (graph/dag のテストと同じ形)。
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
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "blocked", AgentKind: "question"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "B", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "C", CWD: cwd, AgentStatus: "blocked", AgentKind: "permission"},
		),
		Tasks: []record.Task{
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
	if got, want := drawRows(b.Graph.Rows), "* task:A/1\n│ * task:B/1\n├┄┘\n* task:A/2\n* task:A/3\n* task:A/4\n* task:C/1"; got != want {
		t.Errorf("rows =\n%s\nwant\n%s", got, want)
	}
	if got, want := drawRows(b.Graph.RowsWithCompleted), "* task:A/0\n│ * task:A/1\n│ │ * task:B/1\n├─┴┄┘\n* task:A/2\n* task:A/3\n* task:A/4\n* task:C/1"; got != want {
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
	empty := build(input{})
	if empty.Graph.Rows == nil || empty.Graph.RowsWithCompleted == nil {
		t.Errorf("rows = %v / %v, want 空の配列", empty.Graph.Rows, empty.Graph.RowsWithCompleted)
	}
}

// before 側の sid: の端点が末端のタスクを複数持つときは、末端をセッションのノード (sid:) に集め、辺の記録 1 つを
// セッションのノードからの 1 本にする (#405)。セッションのノードは末端がすべて完了したら完了、そうでなければ
// セッションの活動を映す。末端が 1 つならそのタスクへ直接つなぐ。
func TestGraph_sessionSinksHub(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "W", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "P", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "Q", CWD: cwd, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p4", SessionID: "S", CWD: cwd, AgentStatus: "working"},
		),
		Tasks: []record.Task{
			task("W", "10", "#373 の報告待ち", "pending", cwd),
			task("W", "11", "#398 の報告待ち", "pending", cwd),
			task("W", "12", "Q を待つ", "pending", cwd),
			task("W", "13", "S を待つ", "pending", cwd),
			task("P", "1", "#373", "completed", cwd),
			task("P", "2", "#398", "in_progress", cwd),
			task("Q", "1", "a", "completed", cwd),
			task("Q", "2", "b", "completed", cwd),
			task("S", "1", "ひとつ", "in_progress", cwd),
		},
		Edges: []edge.Edge{
			e("e1", "sid:P", "task:W/10"),
			e("e2", "sid:P", "task:W/11"),
			e("e3", "sid:Q", "task:W/12"),
			e("e4", "sid:S", "task:W/13"),
		},
		SessionNames: map[string]string{"P": "peer", "Q": "quiet"},
		Sessions:     []ledger.Session{{SessionID: "P", Name: "at-ci-ui"}},
	})
	want := []string{
		"sid:P>task:W/10", "sid:P>task:W/11",
		"sid:Q>task:W/12",
		"task:P/1>sid:P", "task:P/2>sid:P",
		"task:Q/1>sid:Q", "task:Q/2>sid:Q",
		"task:S/1>task:W/13",
	}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
	for _, l := range b.Graph.Links {
		if l.Before == "sid:P" && (l.Edge == nil || l.Edge.Before.Ref != "sid:P") {
			t.Errorf("セッションのノードからの辺は元の記録を持つ: %+v", l)
		}
		if l.After == "sid:P" && l.Kind != graph.LinkTask {
			t.Errorf("末端からセッションのノードへの辺は実線 (task): %+v", l)
		}
	}
	got := nodes(b.Graph)
	if n := got["sid:P"]; n.Kind != graph.KindRef || n.State != graph.StateWorking || n.SessionID != "P" || n.Title != "peer" || n.SessionName != "at-ci-ui" || !n.Endpoint || !n.Live {
		t.Errorf("sid:P = %+v", n)
	}
	if n := got["sid:Q"]; n.State != graph.StateCompleted {
		t.Errorf("sid:Q state = %q, want completed (末端がすべて完了)", n.State)
	}
	if _, ok := got["sid:S"]; ok {
		t.Errorf("末端が 1 つのセッションにノードを足さない")
	}
}

// 完了したノードを隠した図 (folded) は、隠したノードを通り抜けた辺を畳んで残す (#392)。末端の完了ノードは、
// 完了も終了もしていない上流を直接持つ間だけ見せる。rows も同じ図で組む。
func TestGraph_folded(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "P", CWD: cwd, AgentStatus: "working"},
		),
		Tasks: []record.Task{
			task("A", "1", "始め", "in_progress", cwd),
			task("A", "2", "済んだ中間", "completed", cwd, "1"),
			task("A", "3", "済んだ中間 2", "completed", cwd, "2"),
			task("A", "4", "終わり", "pending", cwd, "3"),
			task("A", "5", "報告を受け取る", "completed", cwd),
			task("A", "6", "済んだ前提", "completed", cwd),
			task("A", "7", "済んだ末端", "completed", cwd, "6"),
			task("X", "1", "終了したセッションを待った", "completed", cwd),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "sid:P", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/5", CWD: cwd}},
			{ID: "e2", Before: edge.Endpoint{Ref: "sid:G", CWD: cwd}, After: edge.Endpoint{Ref: "task:X/1", CWD: cwd}},
		},
	})
	f := b.Graph.Folded
	// A/5 は生きている peer を上流に持つ末端なので残る。X/1 の上流は終了した置き場、A/7 の上流は完了なので隠す。
	// 段はこの図の辺で数え直す (#395)。A/4 は全部の図では 3 段目だが、畳んだ辺で A/1 の次の段になる。
	got := map[string]int{}
	for _, n := range f.Nodes {
		got[n.Key] = n.Depth
	}
	if want := map[string]int{"sid:P": 0, "task:A/1": 0, "task:A/4": 1, "task:A/5": 1, "sid:G": 0}; !reflect.DeepEqual(got, want) {
		t.Errorf("folded nodes = %v, want %v", got, want)
	}
	if n := nodes(b.Graph)["task:A/4"]; n.Depth != 3 {
		t.Errorf("task:A/4 depth = %d, want 3 (全部の図の段は変えない)", n.Depth)
	}
	if want := []protocol.FoldedLink{{Before: "task:A/1", After: "task:A/4", Via: []string{"task:A/2", "task:A/3"}}}; !reflect.DeepEqual(f.Links, want) {
		t.Errorf("folded links = %+v, want %+v", f.Links, want)
	}
	// 畳んだ辺 (A/1 → A/4) と残した末端 (P → A/5) は、それぞれ同じレーンの続く行になる。
	if got, want := drawRows(b.Graph.Rows), "* task:A/1\n* task:A/4\n* sid:G\n* sid:P\n* task:A/5"; got != want {
		t.Errorf("rows =\n%s\nwant\n%s", got, want)
	}
	// ノードが無ければ空の配列 (JSON で null にしない)。
	empty := build(input{})
	if empty.Graph.Folded.Nodes == nil || empty.Graph.Folded.Links == nil {
		t.Errorf("folded = %+v, want 空の配列", empty.Graph.Folded)
	}
}

// まだ引いていない外部の参照 (issue / PR) は daemon が状態を知らないので、原因の鎖 (blocked_by) に選ばず、完了したタスクを
// 隠す図で末端の完了ノードを残す上流にも数えない (#393)。
func TestGraph_externalRef(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"}),
		Tasks: []record.Task{
			task("A", "1", "マージを待つ", "pending", cwd),
			task("A", "2", "マージ後に済んだ", "completed", cwd),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "pr:o/r#389", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/1", CWD: cwd}},
			{ID: "e2", Before: edge.Endpoint{Ref: "pr:o/r#390", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/2", CWD: cwd}},
		},
	})
	got := nodes(b.Graph)
	if n := got["task:A/1"]; len(n.BlockedBy) != 0 {
		t.Errorf("task:A/1 blocked_by = %v, want 空", n.BlockedBy)
	}
	for _, n := range b.Graph.Folded.Nodes {
		if n.Key == "task:A/2" {
			t.Errorf("folded nodes = %v, task:A/2 は外部の参照だけを上流に持つので隠す", b.Graph.Folded.Nodes)
		}
	}
}

// daemon が引いて open だった参照は済んでいないと分かっているので、待ち (waiting) にして原因の鎖に選び、末端の
// 完了ノードを残す上流にも数える (#471)。まだ引いていない参照は外部 (external) のまま鎖から外す。
func TestGraph_waitingRef(t *testing.T) {
	const cwd = "/w/a"
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "P", CWD: cwd, AgentStatus: "working"},
		),
		Tasks: []record.Task{
			task("A", "2", "リリース", "pending", cwd, "3"),
			task("A", "3", "マージを待つ", "pending", cwd),
			task("A", "4", "報告を受けて済んだ", "completed", cwd),
			task("A", "5", "まだ引いていない PR を待つ", "pending", cwd),
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "sid:P", CWD: cwd}, After: edge.Endpoint{Ref: "pr:o/r#470", CWD: cwd}},
			{ID: "e2", Before: edge.Endpoint{Ref: "pr:o/r#470", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/3", CWD: cwd}},
			{ID: "e3", Before: edge.Endpoint{Ref: "pr:o/r#471", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/4", CWD: cwd}},
			{ID: "e4", Before: edge.Endpoint{Ref: "pr:o/r#472", CWD: cwd}, After: edge.Endpoint{Ref: "task:A/5", CWD: cwd}},
		},
		RefStates: map[string]string{"pr:o/r#470": "open", "pr:o/r#471": "open"},
	})
	got := nodes(b.Graph)
	if n := got["pr:o/r#470"]; n.State != graph.StateWaiting || n.Halted || n.Stuck {
		t.Errorf("pr:o/r#470 = %+v, want waiting (止まってはいない)", n)
	}
	if n := got["pr:o/r#472"]; n.State != graph.StateExternal {
		t.Errorf("pr:o/r#472 state = %q, want external (まだ引いていない)", n.State)
	}
	if n := got["task:A/3"]; !reflect.DeepEqual(n.BlockedBy, []string{"pr:o/r#470", "sid:P"}) {
		t.Errorf("task:A/3 blocked_by = %v, want PR から peer へ", n.BlockedBy)
	}
	if n := got["task:A/2"]; !reflect.DeepEqual(n.BlockedBy, []string{"task:A/3", "pr:o/r#470", "sid:P"}) {
		t.Errorf("task:A/2 blocked_by = %v, want PR を通って peer まで", n.BlockedBy)
	}
	if n := got["task:A/5"]; len(n.BlockedBy) != 0 {
		t.Errorf("task:A/5 blocked_by = %v, want 空 (まだ引いていない参照は選ばない)", n.BlockedBy)
	}
	shown := map[string]bool{}
	for _, n := range b.Graph.Folded.Nodes {
		shown[n.Key] = true
	}
	if !shown["task:A/4"] {
		t.Errorf("folded nodes = %v, task:A/4 は open の PR を上流に持つので残す", b.Graph.Folded.Nodes)
	}
}

// 止まっているノードも待ちの輪も無ければ longest chain は空。循環した辺があっても止まらずに組む。
func TestGraph_noBlockedAndCycle(t *testing.T) {
	const cwd = "/w/a"
	calm := build(input{
		Tasks: []record.Task{task("A", "1", "x", "pending", cwd), task("A", "2", "y", "pending", cwd, "1")},
	})
	if len(calm.Graph.LongestChain) != 0 {
		t.Errorf("longest chain = %v, want 空", calm.Graph.LongestChain)
	}
	b := build(input{
		Tasks: []record.Task{
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
		Tasks: []record.Task{
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
	if !got["task:A/1"].Deadlock || !got["task:A/2"].Deadlock || got["task:A/3"].Deadlock || got["task:A/4"].Deadlock {
		t.Errorf("deadlock = %v / %v / %v / %v, want true / true / false / false", got["task:A/1"].Deadlock, got["task:A/2"].Deadlock, got["task:A/3"].Deadlock, got["task:A/4"].Deadlock)
	}
	// 待ちの輪のノードは stuck だが、セッション自身は止まっていないので halted ではない。
	if n := got["task:A/1"]; !n.Stuck || n.Halted {
		t.Errorf("輪のノード halted/stuck = %v/%v, want false/true", n.Halted, n.Stuck)
	}
	if got["task:A/3"].Stuck {
		t.Errorf("輪の下流にいるだけのノードは stuck ではない")
	}
	// 待ちの輪も止まっているノードとして longest chain の起点にする (pane は輪を ■ に数える)。
	if want := []string{"task:A/1", "task:A/2"}; !reflect.DeepEqual(b.Graph.LongestChain, want) {
		t.Errorf("longest chain = %v, want %v", b.Graph.LongestChain, want)
	}
}

// ノードの parked は、そのタスクの途中で置かれた open のメモの数。タスクの無いセッションのノードには、
// タスクを指さずにそのセッションが置いたメモを数える。メモの source は置いたときのタスクをノードの key (task_ref) で出す。
func TestGraph_parked(t *testing.T) {
	const cwd = "/w/a"
	memo := func(id string, sources ...parking.Source) parking.Item {
		return parking.Item{ID: id, Repo: cwd, Status: parking.StatusOpen, Kind: parking.KindAside, Title: id, Sources: sources}
	}
	b := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "S", CWD: cwd, AgentStatus: "working"}),
		Tasks:    []record.Task{task("A", "1", "x", "in_progress", cwd), task("A", "2", "y", "pending", cwd)},
		Parking: []parking.Item{
			memo("m1", parking.Source{SessionID: "A", TaskID: "1", CWD: cwd, At: t0}),
			memo("m2", parking.Source{SessionID: "A", TaskID: "1", CWD: cwd, At: t0}, parking.Source{SessionID: "S", CWD: cwd, At: t0}),
			memo("m3", parking.Source{SessionID: "A", CWD: cwd, At: t0}),
		},
	})
	got := nodes(b.Graph)
	if got["task:A/1"].Parked != 2 || got["task:A/2"].Parked != 0 || got["sid:S"].Parked != 1 {
		t.Errorf("parked = %d / %d / %d, want 2 / 0 / 1", got["task:A/1"].Parked, got["task:A/2"].Parked, got["sid:S"].Parked)
	}
	if s := b.Repos[0].Memos[1].Sources; len(s) != 2 || s[0].TaskRef != "task:A/1" || s[1].TaskRef != "" {
		t.Errorf("source の task_ref = %+v", s)
	}
}

// タスクのノードは description を持つ。pane が自分のタスクをほかのセッションへ渡すときの本文になる (#296)。
func TestGraph_description(t *testing.T) {
	const cwd = "/w/a"
	withDescription := task("A", "1", "x", "in_progress", cwd)
	withDescription.Description = "渡す本文"
	b := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"}),
		Tasks:    []record.Task{withDescription, task("A", "2", "y", "pending", cwd)},
	})
	got := nodes(b.Graph)
	if got["task:A/1"].Description != "渡す本文" {
		t.Errorf("task:A/1 description = %q", got["task:A/1"].Description)
	}
	if got["task:A/2"].Description != "" {
		t.Errorf("task:A/2 description = %q", got["task:A/2"].Description)
	}
}

// タスクの description に書かれたメモの id をメモと照合し、ノードの memos に書かれた順で付ける。open 以外のメモも
// 照合するが、repo の memos と parked には open のメモだけを出す。記録に無い id (消えたメモ) は付けない。
func TestGraph_memos(t *testing.T) {
	const cwd = "/w/a"
	const handed, open, discarded, gone = "20261025T010000-0000000a", "20261025T020000-0000000b", "20261025T030000-0000000c", "20261025T040000-0000000d"
	memo := func(id, title, status string) parking.Item {
		return parking.Item{ID: id, Repo: cwd, Status: status, Origin: parking.OriginAdopted, Kind: parking.KindAside, Title: title,
			Sources: []parking.Source{{SessionID: "A", TaskID: "1", CWD: cwd, At: t0}}}
	}
	withDescription := func(t record.Task, description string) record.Task {
		t.Description = description
		return t
	}
	b := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: cwd, AgentStatus: "working"}),
		Tasks: []record.Task{
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
	if !reflect.DeepEqual(got["task:A/1"].Memos, want) {
		t.Errorf("task:A/1 memos = %+v, want %+v", got["task:A/1"].Memos, want)
	}
	if want := []graph.MemoRef{{ID: discarded, Title: "捨てたメモ", Status: parking.StatusDiscarded}}; !reflect.DeepEqual(got["task:A/2"].Memos, want) {
		t.Errorf("task:A/2 memos = %+v, want %+v", got["task:A/2"].Memos, want)
	}
	if got["task:A/3"].Memos == nil || len(got["task:A/3"].Memos) != 0 {
		t.Errorf("description の無いタスクの memos は空の配列: %#v", got["task:A/3"].Memos)
	}
	if got["task:A/1"].Parked != 1 {
		t.Errorf("parked = %d, want 1 (open のメモだけ数える)", got["task:A/1"].Parked)
	}
	if len(b.Repos) != 1 || len(b.Repos[0].Memos) != 1 || b.Repos[0].Memos[0].ID != open {
		t.Errorf("repo の memos は open のメモだけ: %+v", b.Repos)
	}
	if b.Repos[0].Memos[0].Origin != parking.OriginAdopted {
		t.Errorf("repo のメモの origin = %q", b.Repos[0].Memos[0].Origin)
	}
}
