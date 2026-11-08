package deps

import (
	"reflect"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

func work(sid, cwd, subject string, at time.Time) worklog.Record {
	return worklog.Record{SessionID: sid, CWD: cwd, Subject: subject, Origin: worklog.OriginPrompt, Summary: "最後の応答", Turns: 2, StartedAt: at, UpdatedAt: at}
}

// タスクの無いセッションは作業の記録 (work) からノードにする (#368)。生きていても終了していても載り、題は見せる題
// (人の直した題 > 推定の題 > 最初の prompt)、summary は生きていれば pane の今の作業、終了していれば最後のターンの要約。
// 進行中のタスクのあるセッションには作業のノードを作らない。作業の記録が無い pane は名前から付ける。
func TestBuild_works(t *testing.T) {
	const main = "/src/home/agents-daemon"
	ended := t0.Add(time.Hour)
	exited := work("X", main, "終わった作業", t0)
	exited.EndedAt = &ended
	inferred := work("I", main, "最初の prompt", t0.Add(time.Minute))
	inferred.Inferred = "推定の題"
	human := work("H", main, "最初の prompt", t0.Add(2*time.Minute))
	human.Inferred, human.Subject, human.Origin = "推定の題", "人の題", worklog.OriginHuman
	d := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "L", CWD: main, AgentStatus: "working", AgentMessage: "表を直している"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "I", CWD: main, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "H", CWD: main, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p4", SessionID: "T", CWD: main, AgentStatus: "working", AgentMessage: "タスクの作業"},
			daemonstate.PaneStatus{PaneID: "p5", SessionID: "P", CWD: main, AgentStatus: "working", AgentMessage: "記録の無い作業"},
		),
		Tasks:        []record.Task{task("T", "1", "タスクあり", "in_progress", main)},
		Works:        []worklog.Record{work("L", main, "README を直す", t0.Add(3*time.Minute)), exited, inferred, human, work("T", main, "タスクのあるセッション", t0)},
		Locate:       places(map[string]gitrepo.Place{main: {Repo: main, Branch: "main"}}),
		SessionNames: map[string]string{"L": "live", "X": "gone", "P": "plain"},
	})

	nodes := map[string]protocol.GraphNode{}
	for _, n := range d.Graph.Nodes {
		nodes[n.Key] = n
	}
	type want struct {
		title, origin, summary, state string
		live                          bool
	}
	for key, w := range map[string]want{
		"sid:L":    {"README を直す", "prompt", "表を直している", graph.StateWorking, true},
		"sid:X":    {"終わった作業", "prompt", "最後の応答", graph.StateExited, false},
		"sid:I":    {"推定の題", "inferred", "", graph.StateDone, true},
		"sid:H":    {"人の題", "human", "", graph.StateDone, true},
		"sid:P":    {"plain", "pane", "記録の無い作業", graph.StateWorking, true},
		"task:T/1": {"タスクあり", "", "タスクの作業", graph.StateWorking, true},
	} {
		n, ok := nodes[key]
		if !ok {
			t.Errorf("%s が無い: %v", key, keys(d.Graph.Nodes))
			continue
		}
		got := want{n.Title, n.Origin, n.Summary, n.State, n.Live}
		if got != w {
			t.Errorf("%s = %+v, want %+v", key, got, w)
		}
		if key != "task:T/1" && (n.Kind != graph.KindSession || !n.Endpoint || n.SessionID != key[4:]) {
			t.Errorf("%s = kind %s endpoint %v session %s", key, n.Kind, n.Endpoint, n.SessionID)
		}
	}
	if _, ok := nodes["sid:T"]; ok {
		t.Error("タスクのあるセッションに作業のノードができた")
	}
	if nodes["sid:X"].Session != "gone" {
		t.Errorf("終了したセッションの表示名 = %q", nodes["sid:X"].Session)
	}

	// repo のセッションの見出しは生きているセッションだけで、作業のあるセッションは見せる題、タスクのあるセッションは
	// 進行中のタスク、記録の無いセッションは今の作業。
	subjects := map[string]string{}
	for _, s := range d.Repos[0].Sessions {
		subjects[s.SessionID] = s.Subject
	}
	if want := map[string]string{"L": "README を直す", "I": "推定の題", "H": "人の題", "T": "タスクあり", "P": "記録の無い作業"}; !reflect.DeepEqual(subjects, want) {
		t.Errorf("subjects = %v, want %v", subjects, want)
	}

	// 右の列の順: 作業中は記録の新しい順 (作業の記録の updated_at も使う)。終了は後ろ。
	col := d.Graph.ColumnOrder
	if len(col) != 6 || col[len(col)-1] != "sid:X" || index(col, "sid:L") > index(col, "sid:P") {
		t.Errorf("column_order = %v", col)
	}
}

// 生きていて進行中の作業が無いセッションは、タスクがあっても作業の記録からノードを作り、活動の置き場にする (#489)。
// 終了したセッションと進行中の作業があるセッションには作らない。
func TestBuild_liveSessionWithoutInProgressWork(t *testing.T) {
	const main = "/src/home/agents-daemon"
	ended := t0.Add(time.Hour)
	exited := work("X", main, "終わった作業", t0)
	exited.EndedAt = &ended
	d := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "C", CWD: main, AgentStatus: "blocked", AgentKind: "permission", AgentMessage: "approve Bash: gh pr merge"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "P", CWD: main, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "D", CWD: main, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p4", SessionID: "I", CWD: main, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p5", SessionID: "T", CWD: main, AgentStatus: "working"},
		),
		Tasks: []record.Task{
			task("C", "1", "一", "completed", main),
			task("C", "2", "二", "completed", main),
			task("X", "1", "済んだ", "completed", main),
			task("P", "1", "未着手", "pending", main),
			task("T", "1", "進行中", "in_progress", main),
			task("T", "2", "済んだ", "completed", main),
		},
		Todos: []record.TodoList{
			{SessionID: "D", CWD: main, UpdatedAt: t0, Todos: []record.Todo{{Content: "済んだ", Status: "completed"}}},
			{SessionID: "I", CWD: main, UpdatedAt: t0, Todos: []record.Todo{{Content: "進行中", Status: "in_progress"}}},
		},
		Works: []worklog.Record{
			work("C", main, "PR をマージする", t0), exited, work("P", main, "未着手の作業", t0),
			work("D", main, "Todo の済んだ作業", t0), work("I", main, "Todo の作業", t0), work("T", main, "タスクの作業", t0),
		},
		Locate: places(map[string]gitrepo.Place{main: {Repo: main, Branch: "main"}}),
	})

	got := map[string]protocol.GraphNode{}
	for _, n := range d.Graph.Nodes {
		got[n.Key] = n
	}
	c, ok := got["sid:C"]
	if !ok {
		t.Fatalf("全タスク completed の生きているセッションに sid: のノードが無い: %v", keys(d.Graph.Nodes))
	}
	if c.Kind != graph.KindSession || c.State != graph.StatePermission || !c.Live || c.Title != "PR をマージする" || c.Summary != "approve Bash: gh pr merge" {
		t.Errorf("sid:C = %+v", c)
	}
	if !c.Halted {
		t.Error("permission 待ちのセッションのノードは止まっている扱いになる")
	}
	if col := d.Graph.ColumnOrder; col[0] != "sid:C" {
		t.Errorf("permission 待ちは右の列の先頭に並ぶ: %v", col)
	}
	for key, state := range map[string]string{"sid:P": graph.StateDone, "sid:D": graph.StateDone} {
		if n, ok := got[key]; !ok || n.State != state {
			t.Errorf("%s = %+v (ok %v), want state %s", key, n, ok, state)
		}
	}
	for _, key := range []string{"sid:X", "sid:T", "sid:I"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s は作らない: %v", key, keys(d.Graph.Nodes))
		}
	}
	if got["task:C/1"].State != graph.StateCompleted {
		t.Errorf("タスクのノードの状態は変えない: %+v", got["task:C/1"])
	}
}

// 活動の置き場のノードは辺の端点にしない。タスクのあるセッションの sid: の辺は、ノードの有無によらず同じタスクへ解く。
// 末端がすべて完了なら下流を止めないよう末端からつなぎ、未完了の末端が複数あれば置き場のノードに集める (#405)。
func TestGraph_statusNodeKeepsEdges(t *testing.T) {
	const cwd = "/w/a"
	e := func(id, before, after string) edge.Edge {
		return edge.Edge{ID: id, Before: edge.Endpoint{Ref: before, CWD: cwd}, After: edge.Endpoint{Ref: after, CWD: cwd}}
	}
	b := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "W", CWD: cwd, AgentStatus: "working"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "Q", CWD: cwd, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "R", CWD: cwd, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p4", SessionID: "M", CWD: cwd, AgentStatus: "done"},
		),
		Tasks: []record.Task{
			task("W", "12", "Q を待つ", "pending", cwd),
			task("W", "13", "R を待つ", "pending", cwd),
			task("W", "14", "M を待つ", "pending", cwd),
			task("W", "15", "M を待つ", "pending", cwd),
			task("Q", "1", "a", "completed", cwd),
			task("Q", "2", "b", "completed", cwd),
			task("R", "1", "ひとつ", "completed", cwd),
			task("M", "1", "済んだ", "completed", cwd),
			task("M", "2", "次", "pending", cwd, "1"),
			task("M", "3", "別", "pending", cwd),
		},
		Works: []worklog.Record{work("Q", cwd, "q", t0), work("R", cwd, "r", t0), work("M", cwd, "m", t0)},
		Edges: []edge.Edge{
			e("e1", "sid:Q", "task:W/12"),
			e("e2", "sid:R", "task:W/13"),
			e("e3", "sid:M", "task:W/14"),
			e("e4", "sid:M", "task:W/15"),
			e("e5", "issue:o/r#1", "sid:Q"),
		},
	})
	want := []string{
		"issue:o/r#1>task:Q/1", "issue:o/r#1>task:Q/2",
		"sid:M>task:W/14", "sid:M>task:W/15",
		"task:M/1>task:M/2", "task:M/2>sid:M", "task:M/3>sid:M",
		"task:Q/1>task:W/12", "task:Q/2>task:W/12",
		"task:R/1>task:W/13",
	}
	if !reflect.DeepEqual(links(b.Graph), want) {
		t.Errorf("links = %v, want %v", links(b.Graph), want)
	}
	got := nodes(b.Graph)
	if n := got["sid:Q"]; n.Kind != graph.KindSession || n.State != graph.StateDone {
		t.Errorf("sid:Q = %+v", n)
	}
	if n := got["task:W/12"]; len(n.BlockedBy) != 0 {
		t.Errorf("完了した末端だけを待つ下流は止まらない: %v", n.BlockedBy)
	}
	if n := got["sid:M"]; n.State != graph.StateDone || !n.Live {
		t.Errorf("sid:M = %+v", n)
	}
}

func keys(nodes []protocol.GraphNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Key)
	}
	return out
}

func index(list []string, key string) int {
	for i, k := range list {
		if k == key {
			return i
		}
	}
	return -1
}
