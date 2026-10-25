// Package board はタスクの記録 (internal/tasklog) と status.json のスナップショットを、タスクのボードへ組む (#121)。
//
// 行 (スイムレーン) を repo、列をタスクの状態 (未着手・進行中・ブロック・完了) にし、カード 1 枚がタスク 1 つ。
// worktree のタスクは main worktree と同じ行に入る。セッションの状態 (入力待ち・上限待ち) は、そのセッションの
// 進行中のカードの印にする。どのタスクが人の手を待っているかを 1 枚で見せるため。タスクを作っていない
// 生きているセッションはカード 1 枚で出し、行から消えないようにする。見出しは今の作業 (program_status の msg)、
// 無ければセッション名 (名前も無ければ cwd の末尾)。
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
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/parking"
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
	KindSession = "session" // タスクを作っていない生きているセッション (subject は今の作業か、セッションの名前)
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
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// TaskID は kind task のカードのタスクの ID (park adopt に渡す)。
	TaskID  string `json:"task_id,omitempty"`
	Column  string `json:"column"`
	Subject string `json:"subject"`
	// SessionID はタスクのセッション。Live なら mod はここへ指示を送れる。
	SessionID string `json:"session_id"`
	// Session はカードの末尾に出すセッションの短い名前 (セッション名、無ければ session ID の先頭)。
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

// ParkingCard はパーキングの列のメモ 1 つ (internal/parking の open の Item)。
type ParkingCard struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Detail       string   `json:"detail,omitempty"`
	Files        []string `json:"files,omitempty"`
	DefaultTaken string   `json:"default_taken,omitempty"`
	// Sources は置いたセッションの新しい順。
	Sources []ParkingSource `json:"sources"`
}

// ParkingSource はメモを置いたセッション 1 つ。
type ParkingSource struct {
	SessionID string `json:"session_id"`
	// TaskID は置いたときに進めていたタスク (無ければ省く)。
	TaskID string `json:"task_id,omitempty"`
	// Session はカードと同じ規則で付けたセッションの短い名前。
	Session string `json:"session"`
	Live    bool   `json:"live"`
	// Detail はまとめたときに後から置いた側が書いた detail。
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Row は repo 1 つの行。
type Row struct {
	// Repo は行の鍵 (main worktree のパス、git の外なら cwd)。
	Repo  string `json:"repo"`
	Title string `json:"title"`
	Cards []Card `json:"cards"`
	// Parking はこの repo で置かれた open のメモ (置いた順)。タスクの列とは別の欄にし、mod が列の右に置く。
	Parking []ParkingCard `json:"parking"`
}

// Board は `agents-daemon status --board` が出す JSON の全体。
type Board struct {
	Domain        domain.Domain `json:"domain"`
	DaemonRunning bool          `json:"daemon_running"`
	// UpdatedAt は status.json の更新時刻。daemon が動いていないか、まだ書いていなければ無い。
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Columns   []Column   `json:"columns"`
	// Here は開いたセッションの cwd の行の鍵 (Row.Repo と同じ規則)。mod が自分の repo のメモ (band) を
	// rows から引くのに使う。その repo の行が無ければ rows に無い鍵になる。
	Here string `json:"here"`
	Rows []Row  `json:"rows"`
	// Graph はタスクの依存の DAG (#134)。rows と同じカードとメモから組む。
	Graph Graph `json:"graph"`
}

// input はボードを組む材料。
type input struct {
	Domain domain.Domain
	// CWD は開いたセッションの cwd (Board.Here の元)。
	CWD string
	// PID は動いている daemon の pid。0 なら動いていない。
	PID      int
	Snapshot *daemon.StatusSnapshot
	Tasks    []tasklog.Task
	Todos    []tasklog.TodoList
	// Parking は domain のメモ (status を問わない)。パーキングの列には open のものだけを出し、タスクとの照合には
	// 全部を使う。
	Parking []parking.Item
	// Edges は domain のセッションをまたぐ辺 (internal/edge)。
	Edges []edge.Edge
	// Classify は pane の cwd の domain を決める。
	Classify func(cwd string) domain.Domain
	// Elsewhere はセッションの記録がほかの domain の側にあるか。業務に倒したセッションを、cwd だけで
	// 私用のボードに描かないために使う。nil なら見ない。
	Elsewhere func(sessionID string) bool
	// Locate は cwd の repo を決める。同じ cwd では 1 回だけ呼ぶ。
	Locate func(cwd string) gitrepo.Place
	// SessionNames は session ID → セッション名 (statusline の session_name、sessioncontext.ListSessionNames)。
	SessionNames map[string]string
}

// build はボードを組む。
func build(in input) Board {
	b := Board{Domain: in.Domain, Columns: columns, Rows: []Row{}}
	var panes map[string]*daemon.PaneStatus
	b.DaemonRunning, b.UpdatedAt, panes = livePanes(in)
	cards := collect(in, panes)
	open := parking.Filter(in.Parking, "", parking.StatusOpen)
	names := sessionNames(sessionIDs(cards, open), in.SessionNames)
	l := locator{locate: in.Locate, cache: map[string]gitrepo.Place{}}
	b.Here, _ = l.row(in.CWD)
	b.Rows = arrange(cards, open, panes, names, l)
	b.Graph = buildGraph(in, open, cards, panes, names)
	return b
}

// sessionIDs はカードとメモの source に出てくるセッション。
func sessionIDs(cards []placed, memos []parking.Item) map[string]bool {
	ids := map[string]bool{}
	for _, c := range cards {
		ids[c.card.SessionID] = true
	}
	for _, m := range memos {
		for _, s := range m.Sources {
			ids[s.SessionID] = true
		}
	}
	return ids
}

// livePanes は daemon が動いているか、status.json の更新時刻、このボードに載せる生きているセッションの
// pane (session ID ごと) を返す。前の daemon が残した status.json は使わない (status コマンドと同じ判断)。
func livePanes(in input) (running bool, updatedAt *time.Time, panes map[string]*daemon.PaneStatus) {
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
func collect(in input, panes map[string]*daemon.PaneStatus) []placed {
	var cards []placed
	withTasks := map[string]bool{}
	for _, t := range in.Tasks {
		withTasks[t.SessionID] = true
		p := panes[t.SessionID]
		cards = append(cards, placed{cwd: t.CWD, card: taskCard(t, in.Tasks, p), status: t.Status, pane: p})
	}
	for _, list := range in.Todos {
		withTasks[list.SessionID] = true
		p := panes[list.SessionID]
		for i, todo := range list.Todos {
			cards = append(cards, placed{cwd: list.CWD, card: todoCard(list, i, todo, p), status: todo.Status, pane: p})
		}
	}
	for id, p := range panes {
		if withTasks[id] {
			continue
		}
		c := Card{Key: id + "/session", Kind: KindSession, Column: ColumnInProgress, Subject: sessionSubject(p, in.SessionNames[id]), SessionID: id, Live: true, UpdatedAt: p.LastTickAt}
		mark(&c, p)
		cards = append(cards, placed{cwd: p.CWD, card: c, status: "in_progress", pane: p})
	}
	return cards
}

// sessionSubject はタスクの無いセッションのカードの見出し。Claude Code が申告した今の作業 (作業中ならその作業、
// ターンを終えたら要約、permission 待ちなら approve <tool>: ...) を出し、申告が無ければ (起動直後、古い record)
// どのセッションかが分かるよう、セッション名 (name) か cwd の末尾にする。msg はタスクの subject と同じく漏れては
// いけない情報で、ボードに載るのは自分の domain の pane だけ (livePaneInDomain)。
// cc-premise: 2.1.302 osc7501 msg は作業中の作業、ターン後の要約、permission 待ちの approve <tool>: <command>
func sessionSubject(p *daemon.PaneStatus, name string) string {
	switch {
	case p.AgentMessage != "":
		return p.AgentMessage
	case name != "":
		return name
	default:
		return filepath.Base(p.CWD)
	}
}

// arrange はカードとメモの source にセッションの名前 (names) を付け、repo の行へ分けて並べる。
func arrange(cards []placed, memos []parking.Item, panes map[string]*daemon.PaneStatus, names map[string]string, l locator) []Row {
	byRepo := map[string]*Row{}
	rowOf := func(key, title string) *Row {
		r, ok := byRepo[key]
		if !ok {
			r = &Row{Repo: key, Title: title, Cards: []Card{}, Parking: []ParkingCard{}}
			byRepo[key] = r
		}
		return r
	}
	for _, c := range cards {
		c.card.Session = names[c.card.SessionID]
		r := rowOf(l.row(c.cwd))
		r.Cards = append(r.Cards, c.card)
	}
	for _, m := range memos {
		r := rowOf(m.Repo, filepath.Base(m.Repo))
		r.Parking = append(r.Parking, parkingCard(m, names, panes))
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

// parkingCard はメモをパーキングの列のカードにする。sources は新しい順に並べる。
func parkingCard(m parking.Item, names map[string]string, panes map[string]*daemon.PaneStatus) ParkingCard {
	c := ParkingCard{ID: m.ID, Kind: m.Kind, Title: m.Title, Detail: m.Detail, Files: m.Files, DefaultTaken: m.DefaultTaken, Sources: []ParkingSource{}}
	for _, s := range m.Sources {
		c.Sources = append(c.Sources, ParkingSource{SessionID: s.SessionID, TaskID: s.TaskID, Session: names[s.SessionID], Live: panes[s.SessionID] != nil, Detail: s.Detail, At: s.At})
	}
	sort.SliceStable(c.Sources, func(i, j int) bool { return c.Sources[i].At.After(c.Sources[j].At) })
	return c
}

// placed はカードと、そのカードの行を決める cwd。status はタスクの status (タスクの無いセッションは
// in_progress)、pane はセッションの pane (無ければ nil) で、グラフのノードの状態に使う。
type placed struct {
	cwd    string
	card   Card
	status string
	pane   *daemon.PaneStatus
}

// livePaneInDomain は pane のセッションをこのボードに載せるかを返す。記録がほかの domain の側にあれば
// 載せず (業務に倒したセッション)、無ければ pane の cwd で決める。
func livePaneInDomain(in input, p *daemon.PaneStatus) bool {
	if in.Elsewhere != nil && in.Elsewhere(p.SessionID) {
		return false
	}
	if in.Classify == nil {
		return in.Domain == domain.Work
	}
	return in.Classify(p.CWD) == in.Domain
}

func taskCard(t tasklog.Task, all []tasklog.Task, p *daemon.PaneStatus) Card {
	c := Card{Key: t.SessionID + "/" + t.ID, Kind: KindTask, TaskID: t.ID, Subject: t.Subject, SessionID: t.SessionID, Live: p != nil, UpdatedAt: t.UpdatedAt}
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
