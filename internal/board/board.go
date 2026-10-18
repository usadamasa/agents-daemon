// Package board はタスクの記録 (internal/tasklog) と status.json のスナップショットを、タスクのボードへ組む (#121)。
//
// 行 (スイムレーン) を repo、列をタスクの状態 (未着手・進行中・ブロック・完了) にし、カード 1 枚がタスク 1 つ。
// worktree のタスクは main worktree と同じ行に入る。セッションの状態 (入力待ち・上限待ち) は、そのセッションの
// 進行中のカードの印にする。どのタスクが人の手を待っているかを 1 枚で見せるため。タスクを作っていない
// 生きているセッションは「タスクなし」のカード 1 枚で出し、行から消えないようにする。
//
// ボードは 1 つの domain しか描かない。開いたセッション自身の domain で、ほかの domain のタスクとセッションは
// 載せない (私用と業務を混ぜない)。
//
// 読み手は plugin の mod (hooks/board.tsx) で、`agents-daemon status --board` の JSON を数秒ごとに読んで pane に描く。
// 行と列の分け方と並び順はここが持ち、mod は受け取った順に描くだけにする (JSON を加工する処理は
// バイナリに持たせる、という AGENTS.md の決めごとに合わせる)。
package board

import (
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// 列の ID。mod はこの ID を key にして描く。
const (
	ColumnPending    = "pending"
	ColumnInProgress = "in_progress"
	ColumnBlocked    = "blocked"
	ColumnCompleted  = "completed"
)

// columns はボードの列の順。#121 の「ボードの形」と同じ並び。
var columns = []Column{
	{ID: ColumnPending, Title: "未着手"},
	{ID: ColumnInProgress, Title: "進行中"},
	{ID: ColumnBlocked, Title: "ブロック"},
	{ID: ColumnCompleted, Title: "完了 (直近)"},
}

// カードの種類。
const (
	KindTask    = "task"    // TaskCreate で作られたタスク
	KindTodo    = "todo"    // TodoWrite のリストの 1 項目
	KindSession = "session" // タスクを作っていない生きているセッション
)

// カードの印。セッションが人の手か時間を待っているときに、その進行中のカードに付く。
const (
	MarkInput = "input" // 入力待ち (permission など)
	MarkLimit = "limit" // 利用上限の解除待ち (daemon が再開を待っている)
)

// Column は列の見出し。
type Column struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Card はカード 1 枚。
type Card struct {
	// Key はボード全体で一意な識別子 (<session_id>/<task_id> など)。mod が描画の key と選択に使う。
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Column  string `json:"column"`
	Subject string `json:"subject"`
	// SessionID はタスクのセッション。Live なら mod はここへ指示を送れる。
	SessionID string `json:"session_id"`
	// Session はカードの末尾に出すセッションの短い名前 (branch、無ければ session ID の先頭)。
	Session string `json:"session"`
	// Live はセッションが daemon の見ている pane で動いているか。終了したセッションの未完了のタスクは false。
	Live bool `json:"live"`
	// Blockers は完了していない blockedBy の数 (ブロックの列の ←N)。
	Blockers int    `json:"blockers,omitempty"`
	Mark     string `json:"mark,omitempty"`
	// MarkDetail は印の補足 (入力待ちの種類、上限の解除時刻)。
	MarkDetail string    `json:"mark_detail,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Row は repo 1 つの行。
type Row struct {
	// Repo は行の鍵 (main worktree のパス、git の外なら cwd)。
	Repo  string `json:"repo"`
	Title string `json:"title"`
	Cards []Card `json:"cards"`
}

// Board は `agents-daemon status --board` が出す JSON の全体。
type Board struct {
	Domain        domain.Domain `json:"domain"`
	DaemonRunning bool          `json:"daemon_running"`
	// UpdatedAt は status.json の更新時刻。daemon が動いていないか、まだ書いていなければ無い。
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Columns   []Column   `json:"columns"`
	Rows      []Row      `json:"rows"`
}

// Input はボードを組む材料。
type Input struct {
	Domain domain.Domain
	// PID は動いている daemon の pid。0 なら動いていない。
	PID      int
	Snapshot *daemon.StatusSnapshot
	Tasks    []tasklog.Task
	Todos    []tasklog.TodoList
	// Classify は pane の cwd の domain を決める。
	Classify func(cwd string) domain.Domain
	// Elsewhere はセッションの記録がほかの domain の側にあるか。業務に倒したセッションを、cwd だけで
	// 私用のボードに描かないために使う。nil なら見ない。
	Elsewhere func(sessionID string) bool
	// Locate は cwd の repo と branch を決める。同じ cwd では 1 回だけ呼ぶ。
	Locate func(cwd string) gitrepo.Place
}

// Build はボードを組む。
func Build(in Input) Board {
	b := Board{Domain: in.Domain, Columns: columns, Rows: []Row{}}
	var panes map[string]*daemon.PaneStatus
	b.DaemonRunning, b.UpdatedAt, panes = livePanes(in)
	b.Rows = arrange(collect(in, panes), panes, locator{locate: in.Locate, cache: map[string]gitrepo.Place{}})
	return b
}

// livePanes は daemon が動いているか、status.json の更新時刻、このボードに載せる生きているセッションの
// pane (session ID ごと) を返す。前の daemon が残した status.json は使わない (status コマンドと同じ判断)。
func livePanes(in Input) (running bool, updatedAt *time.Time, panes map[string]*daemon.PaneStatus) {
	panes = map[string]*daemon.PaneStatus{}
	if in.PID == 0 {
		return false, nil, panes
	}
	if in.Snapshot == nil || in.Snapshot.PID != in.PID {
		return true, nil, panes
	}
	updated := in.Snapshot.UpdatedAt
	for i := range in.Snapshot.Panes {
		p := &in.Snapshot.Panes[i]
		if p.SessionID != "" && livePaneInDomain(in, p) {
			panes[p.SessionID] = p
		}
	}
	return true, &updated, panes
}

// collect はタスク、TodoWrite の項目、タスクの無い生きているセッションをカードにする。
func collect(in Input, panes map[string]*daemon.PaneStatus) []placed {
	var cards []placed
	withTasks := map[string]bool{}
	for _, t := range in.Tasks {
		withTasks[t.SessionID] = true
		cards = append(cards, placed{cwd: t.CWD, card: taskCard(t, in.Tasks, panes[t.SessionID])})
	}
	for _, list := range in.Todos {
		withTasks[list.SessionID] = true
		for i, todo := range list.Todos {
			cards = append(cards, placed{cwd: list.CWD, card: todoCard(list, i, todo, panes[list.SessionID])})
		}
	}
	for id, p := range panes {
		if withTasks[id] {
			continue
		}
		c := Card{Key: id + "/session", Kind: KindSession, Column: ColumnInProgress, Subject: "タスクなし", SessionID: id, Live: true, UpdatedAt: p.LastTickAt}
		mark(&c, p)
		cards = append(cards, placed{cwd: p.CWD, card: c})
	}
	return cards
}

// arrange はカードにセッションの名前を付け、repo の行へ分けて並べる。
func arrange(cards []placed, panes map[string]*daemon.PaneStatus, l locator) []Row {
	// セッションの名前は、そのセッションの今の cwd (生きていれば pane、終わっていれば最後のカード) の branch。
	cwds := map[string]string{}
	for _, c := range cards {
		id := c.card.SessionID
		if p := panes[id]; p != nil {
			cwds[id] = p.CWD
		} else {
			cwds[id] = c.cwd
		}
	}
	names := sessionNames(cwds, l)

	byRepo := map[string]*Row{}
	for _, c := range cards {
		c.card.Session = names[c.card.SessionID]
		key, title := l.row(c.cwd)
		r, ok := byRepo[key]
		if !ok {
			r = &Row{Repo: key, Title: title, Cards: []Card{}}
			byRepo[key] = r
		}
		r.Cards = append(r.Cards, c.card)
	}
	rows := make([]Row, 0, len(byRepo))
	for _, r := range byRepo {
		sortCards(r.Cards)
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Title != rows[j].Title {
			return rows[i].Title < rows[j].Title
		}
		return rows[i].Repo < rows[j].Repo
	})
	return rows
}

// placed はカードと、そのカードの行を決める cwd。
type placed struct {
	cwd  string
	card Card
}

// livePaneInDomain は pane のセッションをこのボードに載せるかを返す。記録がほかの domain の側にあれば
// 載せず (業務に倒したセッション)、無ければ pane の cwd で決める。
func livePaneInDomain(in Input, p *daemon.PaneStatus) bool {
	if in.Elsewhere != nil && in.Elsewhere(p.SessionID) {
		return false
	}
	if in.Classify == nil {
		return in.Domain == domain.Work
	}
	return in.Classify(p.CWD) == in.Domain
}

func taskCard(t tasklog.Task, all []tasklog.Task, p *daemon.PaneStatus) Card {
	c := Card{Key: t.SessionID + "/" + t.ID, Kind: KindTask, Subject: t.Subject, SessionID: t.SessionID, Live: p != nil, UpdatedAt: t.UpdatedAt}
	c.Blockers = openBlockers(t, all)
	c.Column = column(t.Status, c.Blockers)
	if c.Column == ColumnInProgress {
		mark(&c, p)
	}
	return c
}

func todoCard(list tasklog.TodoList, i int, todo tasklog.Todo, p *daemon.PaneStatus) Card {
	c := Card{Key: list.SessionID + "/todo-" + strconv.Itoa(i), Kind: KindTodo, Subject: todo.Content, SessionID: list.SessionID, Live: p != nil, UpdatedAt: list.UpdatedAt}
	c.Column = column(todo.Status, 0)
	if c.Column == ColumnInProgress {
		mark(&c, p)
	}
	return c
}

// column はタスクの状態から列を決める。完了していない blocker があれば、状態にかかわらずブロックに置く。
func column(status string, blockers int) string {
	switch {
	case status == "completed":
		return ColumnCompleted
	case blockers > 0:
		return ColumnBlocked
	case status == "in_progress":
		return ColumnInProgress
	default:
		return ColumnPending
	}
}

// openBlockers は t の blockedBy のうち、同じセッションで完了していないタスクの数。記録に無い blocker
// (消された、TaskList から外れた) は止めていないものとして数えない。
// TaskUpdate の addBlockedBy は blocker が完了しても外れないので、完了したかはここで見る。
func openBlockers(t tasklog.Task, all []tasklog.Task) int {
	n := 0
	for _, id := range t.BlockedBy {
		for _, other := range all {
			if other.SessionID == t.SessionID && other.ID == id && other.Status != "completed" {
				n++
				break
			}
		}
	}
	return n
}

// mark はセッションの pane の状態をカードの印にする。上限待ちは agent の自己申告より先に見る。上限で
// 止まった pane は Claude Code の申告では idle や error に見えるが、利用者にとっては「daemon が再開を
// 待っている」が一番知りたいことなので。
func mark(c *Card, p *daemon.PaneStatus) {
	if p == nil {
		return
	}
	if p.MonitorStatus == "waiting" {
		c.Mark = MarkLimit
		if p.WaitUntil != nil {
			c.MarkDetail = p.WaitUntil.Local().Format("15:04")
		}
		return
	}
	if multiplexer.AgentStatus(p.AgentStatus) == multiplexer.AgentStatusBlocked {
		c.Mark = MarkInput
		c.MarkDetail = p.AgentKind
	}
}

// columnOrder は列の並び (sortCards が使う)。
var columnOrder = map[string]int{ColumnPending: 0, ColumnInProgress: 1, ColumnBlocked: 2, ColumnCompleted: 3}

// sortCards はカードを列、セッション、Key の順に並べる。tick ごとに並びが揺れないようにするため。
func sortCards(cards []Card) {
	sort.Slice(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		if a.Column != b.Column {
			return columnOrder[a.Column] < columnOrder[b.Column]
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		return keyLess(a.Key, b.Key)
	})
}

// keyLess は Key を比べる。同じセッションの task ID は数字なので、"10" が "9" より後になるように数で比べる。
func keyLess(a, b string) bool {
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
