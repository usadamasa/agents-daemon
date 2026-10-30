// Package deps はタスクの記録 (tasklog)、status.json のスナップショット、セッションをまたぐ辺 (edge)、
// パーキングのメモ (parking) から依存図を組む (#134)。
//
// 依存図は 2 つを持つ。graph はタスクの依存の DAG (deps/graph)。repos は repo ごとの生きているセッションと
// open のメモで、pane がメモの節を描き、メモを渡す宛先を選ぶのに使う。worktree の作業は main worktree の repo に入る。
//
// 読み手は plugin の mod (hooks/deps.tsx) で、`agents-daemon deps` の JSON を数秒ごとに読んで pane に描く。
// 並び順はここが持ち、mod は受け取った順に描くだけにする (JSON を加工する処理はバイナリに持たせる、という
// AGENTS.md の決めごとに合わせる)。
package deps

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// ParkingCard は repo の open のメモ 1 つ (parking の Item)。
type ParkingCard struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Origin はメモの出自 (placed / adopted、parking)。
	Origin       string   `json:"origin"`
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
	// TaskRef は置いたときに進めていたタスクのノードの key (ref の task:。無ければ省く)。
	TaskRef string `json:"task_ref,omitempty"`
	// Session はノードと同じ規則で付けたセッションの短い名前。
	Session string `json:"session"`
	Live    bool   `json:"live"`
	// Detail はまとめたときに後から置いた側が書いた detail。
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Session は repo で生きているセッション 1 つ (メモを渡す宛先)。
type Session struct {
	SessionID string `json:"session_id"`
	// Session は表示名 (セッション名。同名のセッションがあれば session ID の先頭を足し、名前が無ければ session ID の先頭)。
	Session string `json:"session"`
	// Subject はそのセッションが何をしているか。自動で付いたセッション名だけでは分からないので、宛先の名前に添える。
	Subject string `json:"subject"`
	Live    bool   `json:"live"`
}

// Repo は repo 1 つ。
type Repo struct {
	// Repo は鍵 (main worktree のパス、git の外なら cwd)。パーキングのメモの repo と同じ規則。
	Repo  string `json:"repo"`
	Title string `json:"title"`
	// Sessions はこの repo で作業している生きているセッション (表示名、session ID の順)。
	Sessions []Session `json:"sessions"`
	// Memos はこの repo で置かれた open のメモ (置いた順)。
	Memos []ParkingCard `json:"memos"`
}

// Deps は `agents-daemon deps` が出す JSON の全体。
type Deps struct {
	DaemonRunning bool `json:"daemon_running"`
	// UpdatedAt は status.json の更新時刻。daemon が動いていないか、まだ書いていなければ無い。
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// Here は開いたセッションの cwd の repo の鍵 (Repo.Repo と同じ規則)。mod が自分の repo を repos から引くのに
	// 使う。その repo が無ければ repos に無い鍵になる。
	Here string `json:"here"`
	// StaleMemoDays は open のメモを「古い」とみなす日数 (parking.StaleAfter)。pane はこの日数より前に最後に置かれた
	// メモをまとめて捨てるボタンを出し、`park discard --days` に渡す。
	StaleMemoDays int    `json:"stale_memo_days"`
	Repos         []Repo `json:"repos"`
	// Graph はタスクの依存の DAG (#134)。repos と同じ記録から組む。
	Graph graph.Graph `json:"graph"`
}

// input は依存図を組む材料。
type input struct {
	// CWD は開いたセッションの cwd (Deps.Here の元)。
	CWD string
	// PID は動いている daemon の pid。0 なら動いていない。
	PID      int
	Snapshot *daemonstate.StatusSnapshot
	Tasks    []record.Task
	Todos    []record.TodoList
	// Parking はメモ (status を問わない)。repo の memos には open のものだけを出し、タスクとの照合には全部を使う。
	Parking []parking.Item
	// Edges はセッションをまたぐ辺 (edge)。
	Edges []edge.Edge
	// Locate は cwd の repo を決める。同じ cwd では 1 回だけ呼ぶ。
	Locate func(cwd string) gitrepo.Place
	// SessionNames は session ID → セッション名 (statusline の session_name、sessioncontext.ListSessionNames)。
	SessionNames map[string]string
}

// build は依存図を組む。
func build(in input) Deps {
	d := Deps{Repos: []Repo{}, StaleMemoDays: int(parking.StaleAfter / (24 * time.Hour))}
	var panes map[string]*daemonstate.PaneStatus
	d.DaemonRunning, d.UpdatedAt, panes = livePanes(in)
	cards := collect(in, panes)
	open := parking.Filter(in.Parking, "", parking.StatusOpen)
	names := sessionNames(sessionIDs(cards, open), in.SessionNames)
	for i := range cards {
		cards[i].Session = names[cards[i].SessionID]
	}
	l := locator{locate: in.Locate, cache: map[string]gitrepo.Place{}}
	d.Here, _ = l.row(in.CWD)
	d.Repos = arrange(cards, open, panes, names, in.SessionNames, l)
	d.Graph = graph.Build(graph.Input{
		Cards:        cards,
		Tasks:        in.Tasks,
		Todos:        in.Todos,
		Edges:        in.Edges,
		Memos:        in.Parking,
		Open:         open,
		SessionNames: in.SessionNames,
	})
	return d
}

// sessionIDs は作業とメモの source に出てくるセッション。
func sessionIDs(cards []graph.Card, memos []parking.Item) map[string]bool {
	ids := map[string]bool{}
	for _, c := range cards {
		ids[c.SessionID] = true
	}
	for _, m := range memos {
		for _, s := range m.Sources {
			ids[s.SessionID] = true
		}
	}
	return ids
}

// livePanes は daemon が動いているか、status.json の更新時刻、生きているセッションの
// pane (session ID ごと) を返す。前の daemon が残した status.json は使わない (status コマンドと同じ判断)。
func livePanes(in input) (running bool, updatedAt *time.Time, panes map[string]*daemonstate.PaneStatus) {
	panes = map[string]*daemonstate.PaneStatus{}
	if in.PID == 0 {
		return false, nil, panes
	}
	if in.Snapshot == nil || in.Snapshot.PID != in.PID {
		return true, nil, panes
	}
	updated := in.Snapshot.UpdatedAt
	for i := range in.Snapshot.Panes {
		p := &in.Snapshot.Panes[i]
		if p.SessionID != "" {
			panes[p.SessionID] = p
		}
	}
	return true, &updated, panes
}

// collect はタスク、TodoWrite の項目、タスクの無い生きているセッションをノードの材料にする。表示名 (Session) は
// 呼び手が付ける。
func collect(in input, panes map[string]*daemonstate.PaneStatus) []graph.Card {
	var cards []graph.Card
	acts := activities(panes)
	withTasks := map[string]bool{}
	for _, t := range in.Tasks {
		withTasks[t.SessionID] = true
		a, live := acts[t.SessionID]
		cards = append(cards, graph.Card{Key: ref.Task(t.SessionID, t.ID), Kind: graph.KindTask, TaskID: t.ID, Description: t.Description, Title: t.Subject, SessionID: t.SessionID,
			CWD: t.CWD, Live: live, Status: t.Status, Activity: a.act, Detail: a.detail})
	}
	for _, list := range in.Todos {
		withTasks[list.SessionID] = true
		a, live := acts[list.SessionID]
		for i, todo := range list.Todos {
			cards = append(cards, graph.Card{Key: ref.Todo(list.SessionID, i), Kind: graph.KindTodo, Title: todo.Content, SessionID: list.SessionID,
				CWD: list.CWD, Live: live, Status: todo.Status, Activity: a.act, Detail: a.detail})
		}
	}
	for id, p := range panes {
		if withTasks[id] {
			continue
		}
		a := acts[id]
		cards = append(cards, graph.Card{Key: ref.SessionID(id), Kind: graph.KindSession, Title: sessionSubject(p, in.SessionNames[id]), SessionID: id,
			CWD: p.CWD, Live: true, Status: "in_progress", Activity: a.act, Detail: a.detail})
	}
	return cards
}

// sessionSubject はタスクの無いセッションの見出し。Claude Code が申告した今の作業 (作業中ならその作業、
// ターンを終えたら要約、permission 待ちなら approve <tool>: ...) を出し、申告が無ければ (起動直後、古い record)
// どのセッションかが分かるよう、セッション名 (name) か cwd の末尾にする。
// cc-premise: 2.1.302 osc7501 msg は作業中の作業、ターン後の要約、permission 待ちの approve <tool>: <command>
func sessionSubject(p *daemonstate.PaneStatus, name string) string {
	switch {
	case p.AgentMessage != "":
		return p.AgentMessage
	case name != "":
		return name
	default:
		return filepath.Base(p.CWD)
	}
}

// arrange は生きているセッションとメモを repo へ分けて並べる。セッションは作業の cwd の repo に入る (タスクの無い
// セッションは pane の cwd)。names はセッションの表示名、sessions は session ID → セッション名。
func arrange(cards []graph.Card, memos []parking.Item, panes map[string]*daemonstate.PaneStatus, names, sessions map[string]string, l locator) []Repo {
	type entry struct {
		repo *Repo
		// subjects はこの repo のセッションごとの見出しの候補。
		subjects map[string]*subjectPick
	}
	byRepo := map[string]*entry{}
	repoOf := func(key, title string) *entry {
		e, ok := byRepo[key]
		if !ok {
			e = &entry{repo: &Repo{Repo: key, Title: title, Sessions: []Session{}, Memos: []ParkingCard{}}, subjects: map[string]*subjectPick{}}
			byRepo[key] = e
		}
		return e
	}
	for _, c := range cards {
		e := repoOf(l.row(c.CWD))
		if !c.Live {
			continue
		}
		pick, ok := e.subjects[c.SessionID]
		if !ok {
			pick = &subjectPick{}
			e.subjects[c.SessionID] = pick
		}
		pick.offer(c)
	}
	for _, m := range memos {
		e := repoOf(m.Repo, filepath.Base(m.Repo))
		e.repo.Memos = append(e.repo.Memos, parkingCard(m, names, panes))
	}
	out := make([]Repo, 0, len(byRepo))
	for _, e := range byRepo {
		for id, pick := range e.subjects {
			subject := pick.title
			if !pick.found {
				subject = sessionSubject(panes[id], sessions[id])
			}
			e.repo.Sessions = append(e.repo.Sessions, Session{SessionID: id, Session: names[id], Subject: subject, Live: true})
		}
		// tick ごとに並びが揺れないよう、表示名、session ID の順に並べる。宛先のボタンはこの順に出る。
		sort.Slice(e.repo.Sessions, func(i, j int) bool {
			a, b := e.repo.Sessions[i], e.repo.Sessions[j]
			if a.Session != b.Session {
				return a.Session < b.Session
			}
			return a.SessionID < b.SessionID
		})
		out = append(out, *e.repo)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// subjectPick は repo の中のセッション 1 つの見出しを選ぶ。進行中のタスク、進行中の TodoWrite の項目の順に選び、
// 同じ種類なら Key の順 (graph.KeyLess) で最初のもの。どちらも無ければ (found が false) 呼び手が pane の今の作業、
// セッション名、cwd の末尾に倒す (sessionSubject)。どの作業が先に来ても同じものを選ぶ。
type subjectPick struct {
	found bool
	rank  int
	key   string
	title string
}

// subjectRank は見出しに選ぶ順。進行中でないものは選ばない (ok が false)。
func subjectRank(c graph.Card) (rank int, ok bool) {
	if c.Status != "in_progress" {
		return 0, false
	}
	switch c.Kind {
	case graph.KindTask:
		return 0, true
	case graph.KindTodo:
		return 1, true
	default:
		return 0, false
	}
}

func (s *subjectPick) offer(c graph.Card) {
	rank, ok := subjectRank(c)
	if !ok {
		return
	}
	if s.found && (rank > s.rank || (rank == s.rank && !graph.KeyLess(c.Key, s.key))) {
		return
	}
	*s = subjectPick{found: true, rank: rank, key: c.Key, title: c.Title}
}

// parkingCard はメモを repo の memos の 1 つにする。sources は新しい順に並べる。
func parkingCard(m parking.Item, names map[string]string, panes map[string]*daemonstate.PaneStatus) ParkingCard {
	c := ParkingCard{ID: m.ID, Kind: m.Kind, Origin: m.Origin, Title: m.Title, Detail: m.Detail, Files: m.Files, DefaultTaken: m.DefaultTaken, Sources: []ParkingSource{}}
	for _, s := range m.Sources {
		var task string
		if s.TaskID != "" {
			task = ref.Task(s.SessionID, s.TaskID)
		}
		c.Sources = append(c.Sources, ParkingSource{SessionID: s.SessionID, TaskRef: task, Session: names[s.SessionID], Live: panes[s.SessionID] != nil, Detail: s.Detail, At: s.At})
	}
	sort.SliceStable(c.Sources, func(i, j int) bool { return c.Sources[i].At.After(c.Sources[j].At) })
	return c
}
