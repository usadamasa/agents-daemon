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
