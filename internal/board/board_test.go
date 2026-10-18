package board

import (
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

var t0 = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// places は cwd → Place の表を Locate にする。表に無い cwd は git の外。
func places(m map[string]gitrepo.Place) func(string) gitrepo.Place {
	return func(cwd string) gitrepo.Place { return m[cwd] }
}

func homeUnder(prefix string) func(string) domain.Domain {
	return func(cwd string) domain.Domain {
		if strings.HasPrefix(cwd, prefix) {
			return domain.Home
		}
		return domain.Work
	}
}

func task(sid, id, subject, status, cwd string, blockedBy ...string) tasklog.Task {
	return tasklog.Task{ID: id, Subject: subject, Status: status, SessionID: sid, CWD: cwd, BlockedBy: blockedBy, UpdatedAt: t0}
}

func snapshot(panes ...daemon.PaneStatus) *daemon.StatusSnapshot {
	return &daemon.StatusSnapshot{PID: 42, UpdatedAt: t0, Panes: panes}
}

// byKey はボードのカードを Key で引けるようにする。行の見出しも返す。
func byKey(b Board) map[string]struct {
	Card
	Row string
} {
	m := map[string]struct {
		Card
		Row string
	}{}
	for _, r := range b.Rows {
		for _, c := range r.Cards {
			m[c.Key] = struct {
				Card
				Row string
			}{c, r.Title}
		}
	}
	return m
}

func TestBuild_rowsAndColumns(t *testing.T) {
	const main, wt, other = "/src/home/agents-daemon", "/src/home/agents-daemon/.claude/worktrees/x", "/src/home/dotfile"
	b := Build(Input{
		Domain:   domain.Home,
		PID:      42,
		Snapshot: snapshot(daemon.PaneStatus{PaneID: "p1", SessionID: "A", CWD: main, AgentStatus: "working"}),
		Tasks: []tasklog.Task{
			task("A", "1", "調査", "completed", main),
			task("A", "2", "ingest-task を書く", "in_progress", main),
			task("A", "3", "PR を出す", "pending", main, "2"),
			task("A", "4", "status の色分け", "pending", main, "1"), // blocker は完了済み
			task("B", "1", "CI を直す", "in_progress", wt),
			task("C", "1", "alias を整理", "pending", other),
		},
		Classify: homeUnder("/src/home"),
		Locate: places(map[string]gitrepo.Place{
			main:  {Repo: main, Branch: "main"},
			wt:    {Repo: main, Branch: "fix-ci"},
			other: {Repo: other, Branch: "main"},
		}),
	})

	if len(b.Rows) != 2 || b.Rows[0].Title != "agents-daemon" || b.Rows[1].Title != "dotfile" {
		t.Fatalf("rows = %+v", b.Rows)
	}
	got := byKey(b)
	want := map[string]struct{ column, row string }{
		"A/1": {ColumnCompleted, "agents-daemon"},
		"A/2": {ColumnInProgress, "agents-daemon"},
		"A/3": {ColumnBlocked, "agents-daemon"},
		"A/4": {ColumnPending, "agents-daemon"},
		"B/1": {ColumnInProgress, "agents-daemon"}, // worktree は main worktree の行
		"C/1": {ColumnPending, "dotfile"},
	}
	for key, w := range want {
		c, ok := got[key]
		if !ok || c.Column != w.column || c.Row != w.row {
			t.Errorf("%s = column %q row %q, want %q / %q", key, c.Column, c.Row, w.column, w.row)
		}
	}
	if got["A/3"].Blockers != 1 || got["A/4"].Blockers != 0 {
		t.Errorf("blockers = %d / %d, want 1 / 0", got["A/3"].Blockers, got["A/4"].Blockers)
	}
	if !got["A/2"].Live || got["B/1"].Live {
		t.Errorf("Live: A は pane があり B は無い")
	}
	// 同じ branch 名 (main) のセッションが 2 つあるので session ID の先頭で見分ける。
	if got["A/2"].Session != "main A" || got["B/1"].Session != "fix-ci" || got["C/1"].Session != "main C" {
		t.Errorf("session names = %q / %q / %q", got["A/2"].Session, got["B/1"].Session, got["C/1"].Session)
	}
	// 列の順 (未着手 → 進行中 → ブロック → 完了)、列の中はセッションの名前の順に並ぶ。
	var order []string
	for _, c := range b.Rows[0].Cards {
		order = append(order, c.Key)
	}
	if strings.Join(order, ",") != "A/4,B/1,A/2,A/3,A/1" {
		t.Errorf("order = %v", order)
	}
}

// セッションの状態は進行中のカードの印になる。上限待ちは自己申告より先に見る。
func TestBuild_marks(t *testing.T) {
	until := time.Date(2026, 10, 18, 14, 30, 0, 0, time.Local)
	b := Build(Input{
		Domain: domain.Work,
		PID:    42,
		Snapshot: snapshot(
			daemon.PaneStatus{PaneID: "p1", SessionID: "A", CWD: "/w/a", AgentStatus: "blocked", AgentKind: "permission"},
			daemon.PaneStatus{PaneID: "p2", SessionID: "B", CWD: "/w/b", AgentStatus: "idle", MonitorStatus: "waiting", WaitUntil: &until},
		),
		Tasks: []tasklog.Task{
			task("A", "1", "書く", "in_progress", "/w/a"),
			task("A", "2", "次", "pending", "/w/a"),
			task("B", "1", "直す", "in_progress", "/w/b"),
		},
	})
	got := byKey(b)
	if got["A/1"].Mark != MarkInput || got["A/1"].MarkDetail != "permission" {
		t.Errorf("A/1 = %+v, want 入力待ち", got["A/1"].Card)
	}
	if got["A/2"].Mark != "" {
		t.Errorf("未着手のカードに印が付いた: %+v", got["A/2"].Card)
	}
	if got["B/1"].Mark != MarkLimit || got["B/1"].MarkDetail != "14:30" {
		t.Errorf("B/1 = %+v, want 上限待ち 14:30", got["B/1"].Card)
	}
}

// タスクを作っていない生きているセッションは「タスクなし」のカードで出す。TodoWrite のリストもカードになる。
func TestBuild_sessionsWithoutTasksAndTodos(t *testing.T) {
	b := Build(Input{
		Domain: domain.Work,
		PID:    42,
		Snapshot: snapshot(
			daemon.PaneStatus{PaneID: "p1", SessionID: "A", CWD: "/w/a", AgentStatus: "blocked"},
			daemon.PaneStatus{PaneID: "p2", SessionID: "T", CWD: "/w/t", AgentStatus: "working"},
			daemon.PaneStatus{PaneID: "p3", CWD: "/w/x", AgentStatus: "working"}, // セッション不明は載せない
		),
		Todos: []tasklog.TodoList{{SessionID: "T", CWD: "/w/t", UpdatedAt: t0, Todos: []tasklog.Todo{
			{Content: "a", Status: "completed"}, {Content: "b", Status: "in_progress"},
		}}},
	})
	got := byKey(b)
	if len(got) != 3 {
		t.Fatalf("cards = %+v", got)
	}
	if c := got["A/session"]; c.Kind != KindSession || c.Subject != "タスクなし" || c.Column != ColumnInProgress || c.Mark != MarkInput || !c.Live {
		t.Errorf("A/session = %+v", c.Card)
	}
	if got["T/todo-0"].Column != ColumnCompleted || got["T/todo-1"].Column != ColumnInProgress || got["T/todo-1"].Kind != KindTodo {
		t.Errorf("todos = %+v / %+v", got["T/todo-0"].Card, got["T/todo-1"].Card)
	}
}

// ボードは 1 つの domain しか描かない。ほかの domain の pane と、業務に倒したセッションは載せない。
func TestBuild_domain(t *testing.T) {
	b := Build(Input{
		Domain: domain.Home,
		PID:    42,
		Snapshot: snapshot(
			daemon.PaneStatus{PaneID: "p1", SessionID: "H", CWD: "/src/home/a"},
			daemon.PaneStatus{PaneID: "p2", SessionID: "W", CWD: "/work/b"},
			daemon.PaneStatus{PaneID: "p3", SessionID: "M", CWD: "/src/home/c"}, // 記録が業務の側にある
		),
		Classify:  homeUnder("/src/home"),
		Elsewhere: func(id string) bool { return id == "M" },
	})
	got := byKey(b)
	if len(got) != 1 || got["H/session"].SessionID != "H" {
		t.Errorf("cards = %+v, want H だけ", got)
	}
	if b.Domain != domain.Home {
		t.Errorf("Domain = %q", b.Domain)
	}
}

// daemon が動いていなくてもタスクの記録は描く。前の daemon の status.json は使わない。
func TestBuild_daemonState(t *testing.T) {
	tasks := []tasklog.Task{task("A", "1", "書く", "in_progress", "/w/a")}
	stopped := Build(Input{Domain: domain.Work, Tasks: tasks, Snapshot: snapshot(daemon.PaneStatus{SessionID: "A", CWD: "/w/a"})})
	if stopped.DaemonRunning || stopped.UpdatedAt != nil || len(stopped.Rows) != 1 || stopped.Rows[0].Cards[0].Live {
		t.Errorf("stopped = %+v", stopped)
	}
	stale := Build(Input{Domain: domain.Work, PID: 7, Tasks: tasks, Snapshot: snapshot(daemon.PaneStatus{SessionID: "A", CWD: "/w/a"})})
	if !stale.DaemonRunning || stale.UpdatedAt != nil || stale.Rows[0].Cards[0].Live {
		t.Errorf("stale = %+v", stale)
	}
	if len(stale.Columns) != 4 || stale.Columns[0].ID != ColumnPending {
		t.Errorf("columns = %+v", stale.Columns)
	}
	// git の外の cwd は cwd そのものを行にする。cwd が無ければ「cwd 不明」の行。
	odd := Build(Input{Domain: domain.Work, Tasks: []tasklog.Task{task("A", "1", "x", "pending", "")}})
	if odd.Rows[0].Title != "(cwd 不明)" || odd.Rows[0].Cards[0].Session != "A" {
		t.Errorf("odd = %+v", odd.Rows)
	}
}

func TestKeyLess(t *testing.T) {
	if !keyLess("A/9", "A/10") || keyLess("A/10", "A/9") || !keyLess("A/1", "B/0") || !keyLess("A/session", "A/todo-0") {
		t.Errorf("keyLess の並びが合わない")
	}
}
