package deps

import (
	"reflect"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

var t0 = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// places は cwd → Place の表を Locate にする。表に無い cwd は git の外。
func places(m map[string]gitrepo.Place) func(string) gitrepo.Place {
	return func(cwd string) gitrepo.Place { return m[cwd] }
}

func task(sid, id, subject, status, cwd string, blockedBy ...string) tasklog.Task {
	return tasklog.Task{ID: id, Subject: subject, Status: status, SessionID: sid, CWD: cwd, BlockedBy: blockedBy, UpdatedAt: t0}
}

func snapshot(panes ...daemonstate.PaneStatus) *daemonstate.StatusSnapshot {
	return &daemonstate.StatusSnapshot{PID: 42, UpdatedAt: t0, Panes: panes}
}

// titles は repo の見出しの並び。
func titles(d Deps) []string {
	out := []string{}
	for _, r := range d.Repos {
		out = append(out, r.Title)
	}
	return out
}

// repos は repo を見出しで引けるようにする。
func repos(d Deps) map[string]Repo {
	m := map[string]Repo{}
	for _, r := range d.Repos {
		m[r.Title] = r
	}
	return m
}

// repo の並びは見出し、鍵の順。worktree のタスクは main worktree の repo に入る。sessions はその repo で生きている
// セッションだけを、表示名、session ID の順に並べる。同じ名前のセッションは session ID の先頭で見分ける。
func TestBuild_repoSessions(t *testing.T) {
	const main, wt, other = "/src/home/agents-daemon", "/src/home/agents-daemon/.claude/worktrees/x", "/src/home/dotfile"
	d := build(input{
		PID: 42,
		Snapshot: snapshot(
			daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: main, AgentStatus: "working", AgentMessage: "別の作業"},
			daemonstate.PaneStatus{PaneID: "p2", SessionID: "D", CWD: wt, AgentStatus: "working", AgentMessage: "調べている"},
			daemonstate.PaneStatus{PaneID: "p3", SessionID: "E", CWD: other, AgentStatus: "done"},
			daemonstate.PaneStatus{PaneID: "p4", SessionID: "T", CWD: other, AgentStatus: "working"},
		),
		Tasks: []tasklog.Task{
			task("A", "1", "調査", "completed", main),
			task("A", "3", "PR を出す", "pending", main, "2"),
			task("A", "10", "後の進行中", "in_progress", main),
			task("A", "2", "ingest-task を書く", "in_progress", main),
			task("B", "1", "CI を直す", "in_progress", wt), // pane が無い (終了した)
			task("C", "1", "alias を整理", "pending", other),
			task("D", "1", "PR を出す", "pending", wt),
		},
		Todos: []tasklog.TodoList{{SessionID: "T", CWD: other, UpdatedAt: t0, Todos: []tasklog.Todo{
			{Content: "済んだ項目", Status: "completed"}, {Content: "進めている項目", Status: "in_progress"},
		}}},
		Locate: places(map[string]gitrepo.Place{
			main:  {Repo: main, Branch: "main"},
			wt:    {Repo: main, Branch: "fix-ci"},
			other: {Repo: other, Branch: "main"},
		}),
		SessionNames: map[string]string{"A": "workspace", "B": "fix-ci", "C": "workspace", "D": "alpha", "T": "todo"},
	})

	if want := []string{"agents-daemon", "dotfile"}; !reflect.DeepEqual(titles(d), want) {
		t.Fatalf("repos = %v, want %v", titles(d), want)
	}
	got := repos(d)
	// 進行中のタスクがあればその subject (Key の順で最初のもの)、無ければ今の作業 (msg)、セッション名、cwd の末尾。
	// TodoWrite だけのセッションは進行中の項目。
	want := map[string][]Session{
		"agents-daemon": {
			{SessionID: "D", Session: "alpha", Subject: "調べている", Live: true},
			{SessionID: "A", Session: "workspace A", Subject: "ingest-task を書く", Live: true},
		},
		"dotfile": {
			{SessionID: "E", Session: "E", Subject: "dotfile", Live: true},
			{SessionID: "T", Session: "todo", Subject: "進めている項目", Live: true},
		},
	}
	for title, w := range want {
		if !reflect.DeepEqual(got[title].Sessions, w) {
			t.Errorf("%s sessions = %+v, want %+v", title, got[title].Sessions, w)
		}
	}
	if got["agents-daemon"].Repo != main {
		t.Errorf("repo = %q, want %q", got["agents-daemon"].Repo, main)
	}
}

// パーキングのメモは repo の memos に並ぶ。タスクの無い repo のメモも repo になる。
// sources は新しい順で、セッションの名前はノードと同じ規則で付ける。
func TestBuild_memos(t *testing.T) {
	const main, wt, other = "/src/home/agents-daemon", "/src/home/agents-daemon/.claude/worktrees/x", "/src/home/dotfile"
	memo := parking.Item{
		ID: "20261018T090000-aaaa", Repo: main, Status: parking.StatusOpen,
		Entry: parking.Entry{Kind: parking.KindAside, Title: "保持期間の根拠が無い", Detail: "d", Files: []string{"a.go"}, DefaultTaken: "やらない"},
		Sources: []parking.Source{
			{SessionID: "B", TaskID: "1", CWD: wt, At: t0},
			{SessionID: "A", CWD: main, Detail: "別の補足", At: t0.Add(time.Hour)},
		},
	}
	lone := parking.Item{
		ID: "20261018T100000-bbbb", Repo: other, Status: parking.StatusOpen,
		Entry:   parking.Entry{Kind: parking.KindPreference, Title: "alias の命名"},
		Sources: []parking.Source{{SessionID: "C", CWD: other, At: t0}},
	}
	d := build(input{
		PID:      42,
		Snapshot: snapshot(daemonstate.PaneStatus{PaneID: "p1", SessionID: "A", CWD: main}),
		Tasks:    []tasklog.Task{task("A", "1", "調査", "in_progress", main)},
		Parking:  []parking.Item{memo, lone},
		Locate: places(map[string]gitrepo.Place{
			main:  {Repo: main, Branch: "main"},
			wt:    {Repo: main, Branch: "fix-ci"},
			other: {Repo: other, Branch: "alias"},
		}),
		SessionNames: map[string]string{"A": "park-a", "B": "park-b"},
	})

	if want := []string{"agents-daemon", "dotfile"}; !reflect.DeepEqual(titles(d), want) {
		t.Fatalf("repos = %v, want %v", titles(d), want)
	}
	if r := d.Repos[1]; r.Sessions == nil || len(r.Sessions) != 0 || len(r.Memos) != 1 || r.Memos[0].ID != lone.ID {
		t.Errorf("メモだけの repo = %+v", r)
	}
	got := d.Repos[0].Memos
	if len(got) != 1 {
		t.Fatalf("memos = %+v", got)
	}
	p := got[0]
	if p.ID != memo.ID || p.Kind != parking.KindAside || p.Title != memo.Title || p.Detail != "d" || p.DefaultTaken != "やらない" || len(p.Files) != 1 {
		t.Errorf("memo = %+v", p)
	}
	if len(p.Sources) != 2 || p.Sources[0].SessionID != "A" || p.Sources[0].Detail != "別の補足" || p.Sources[1].SessionID != "B" {
		t.Fatalf("sources = %+v, want 新しい順", p.Sources)
	}
	if p.Sources[0].Session != "park-a" || !p.Sources[0].Live || p.Sources[1].Session != "park-b" || p.Sources[1].Live {
		t.Errorf("source の名前と Live = %+v", p.Sources)
	}
	// 名前の記録が無いセッションは session ID の先頭に倒す。
	if d.Repos[1].Memos[0].Sources[0].Session != "C" {
		t.Errorf("名前の無いセッションの名前 = %+v", d.Repos[1].Memos[0].Sources)
	}
}

// daemon が動いていなくてもタスクの記録は描く。前の daemon の status.json は使わない。
func TestBuild_daemonState(t *testing.T) {
	tasks := []tasklog.Task{task("A", "1", "書く", "in_progress", "/w/a")}
	stopped := build(input{Tasks: tasks, Snapshot: snapshot(daemonstate.PaneStatus{SessionID: "A", CWD: "/w/a"})})
	if stopped.DaemonRunning || stopped.UpdatedAt != nil || len(stopped.Repos) != 1 || len(stopped.Repos[0].Sessions) != 0 || stopped.Graph.Nodes[0].Live {
		t.Errorf("stopped = %+v", stopped)
	}
	stale := build(input{PID: 7, Tasks: tasks, Snapshot: snapshot(daemonstate.PaneStatus{SessionID: "A", CWD: "/w/a"})})
	if !stale.DaemonRunning || stale.UpdatedAt != nil || len(stale.Repos[0].Sessions) != 0 || stale.Graph.Nodes[0].Live {
		t.Errorf("stale = %+v", stale)
	}
	// git の外の cwd は cwd そのものを repo にする。cwd が無ければ「cwd 不明」の repo。
	odd := build(input{Tasks: []tasklog.Task{task("A", "1", "x", "pending", "")}})
	if odd.Repos[0].Title != "(cwd 不明)" || odd.Graph.Nodes[0].Session != "A" {
		t.Errorf("odd = %+v / %+v", odd.Repos, odd.Graph.Nodes)
	}
	// 何も無ければ空の配列 (JSON で null にしない)。
	if empty := build(input{}); empty.Repos == nil {
		t.Errorf("repos = nil, want 空の配列")
	}
}
