package deps

import (
	"reflect"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
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
// タスクのあるセッションには作業のノードを作らない (辺の端点の解き方を変えない)。作業の記録が無い pane は名前から付ける。
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
