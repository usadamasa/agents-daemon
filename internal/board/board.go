// Package board は status.json のスナップショットを、セッションをカードにしたスイムレーンのボードへ組み直す。
//
// 読み手は plugin の mod (hooks/board.tsx) で、`agents-daemon status --board` の JSON を数秒ごとに読んで pane に描く。
// レーンの分け方と並び順はここが持ち、mod は受け取った順に描くだけにする (JSON を加工する処理は
// バイナリに持たせる、という AGENTS.md の決めごとに合わせる)。
package board

import (
	"encoding/json"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// レーンの ID。mod はこの ID を key にして描く。
const (
	LaneBlocked = "blocked"
	LaneWaiting = "waiting"
	LaneWorking = "working"
	LaneIdle    = "idle"
	LaneOther   = "other"
)

// lanes はボードに並べるレーンの順。利用者の手が要るもの (blocked) を先頭に置く。
var lanes = []struct{ id, title string }{
	{LaneBlocked, "入力待ち"},
	{LaneWaiting, "上限待ち"},
	{LaneWorking, "作業中"},
	{LaneIdle, "待機"},
	{LaneOther, "その他"},
}

// Card はボードのカード 1 枚 (Claude Code が動いている pane 1 つ)。
type Card struct {
	PaneID string `json:"pane_id"`
	// SessionID はボードから指示を送る宛先。pane と紐づいていなければ空で、mod は送信を出さない。
	SessionID   string     `json:"session_id,omitempty"`
	CWD         string     `json:"cwd"`
	Project     string     `json:"project"`
	AgentStatus string     `json:"agent_status"`
	AgentKind   string     `json:"agent_kind,omitempty"`
	Message     string     `json:"message,omitempty"`
	WaitUntil   *time.Time `json:"wait_until,omitempty"`
}

// Lane はレーン 1 本。Cards が空でもレーンは出す (mod が列の並びを固定で描けるように)。
type Lane struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cards []Card `json:"cards"`
}

// Board は `agents-daemon status --board` が出す JSON の全体。
type Board struct {
	// DaemonRunning が false のとき、Lanes のカードは空で UpdatedAt は無い。
	DaemonRunning bool       `json:"daemon_running"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	Lanes         []Lane     `json:"lanes"`
}

// Write は daemon の pid と status.json からボードを組み、JSON で w へ書く。
// daemon が動いていなければ daemon_running が false の空のボードを書き、エラーにはしない
// (mod が「daemon が動いていない」と描けるように)。
func Write(w io.Writer, p apppath.Paths) error {
	pid, err := daemon.ReadPIDFile(p.PIDFile())
	if err != nil {
		return err
	}
	if !daemon.PIDAlive(pid) {
		pid = 0
	}
	var snap *daemon.StatusSnapshot
	if pid != 0 {
		if snap, err = daemon.ReadStatusSnapshot(p.StatusFile()); err != nil {
			return err
		}
	}
	return json.NewEncoder(w).Encode(Build(pid, snap))
}

// Build は daemon の pid と status.json のスナップショットからボードを組む。
//
// snap.PID が pid と違うスナップショットは前の daemon が残したものなので使わない
// (status コマンドの printSnapshot と同じ判断)。pid が 0 なら daemon は動いていない。
func Build(pid int, snap *daemon.StatusSnapshot) Board {
	b := Board{Lanes: make([]Lane, 0, len(lanes))}
	byID := map[string]*Lane{}
	for _, l := range lanes {
		b.Lanes = append(b.Lanes, Lane{ID: l.id, Title: l.title, Cards: []Card{}})
	}
	for i := range b.Lanes {
		byID[b.Lanes[i].ID] = &b.Lanes[i]
	}
	if pid == 0 {
		return b
	}
	b.DaemonRunning = true
	if snap == nil || snap.PID != pid {
		return b
	}
	updated := snap.UpdatedAt
	b.UpdatedAt = &updated

	for _, p := range snap.Panes {
		lane := byID[laneOf(p)]
		lane.Cards = append(lane.Cards, Card{
			PaneID:      p.PaneID,
			SessionID:   p.SessionID,
			CWD:         p.CWD,
			Project:     project(p.CWD),
			AgentStatus: p.AgentStatus,
			AgentKind:   p.AgentKind,
			Message:     p.AgentMessage,
			WaitUntil:   p.WaitUntil,
		})
	}
	for i := range b.Lanes {
		sortCards(b.Lanes[i].Cards)
	}
	return b
}

// laneOf は pane をどのレーンに置くかを決める。上限待ちは agent の自己申告より先に見る。
// 上限で止まった pane は Claude Code の申告では idle や error に見えるが、利用者にとっては
// 「daemon が再開を待っている」が一番知りたいことなので。
func laneOf(p daemon.PaneStatus) string {
	if p.MonitorStatus == "waiting" {
		return LaneWaiting
	}
	switch multiplexer.AgentStatus(p.AgentStatus) {
	case multiplexer.AgentStatusBlocked:
		return LaneBlocked
	case multiplexer.AgentStatusWorking:
		return LaneWorking
	case multiplexer.AgentStatusIdle, multiplexer.AgentStatusDone:
		return LaneIdle
	default:
		return LaneOther
	}
}

// project はカードの見出しに出す名前 (cwd の末尾)。cwd が無ければ空。
func project(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// sortCards はカードを project、pane ID の順に並べる。tick ごとに並びが揺れないようにするため。
func sortCards(cards []Card) {
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Project != cards[j].Project {
			return cards[i].Project < cards[j].Project
		}
		return cards[i].PaneID < cards[j].PaneID
	})
}
