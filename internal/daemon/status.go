package daemon

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// PaneStatus は status.json に書き出す pane 1 枚分のスナップショット。
type PaneStatus struct {
	PaneID string `json:"pane_id"`
	// SessionID は pane で動いている Claude セッションの ID。紐づいていなければ省く。
	// ボード (internal/board) がタスクとセッションを突き合わせ、指示を送る宛先にする。
	SessionID   string `json:"session_id,omitempty"`
	CWD         string `json:"cwd"`
	AgentStatus string `json:"agent_status"`
	// AgentKind と AgentMessage は Claude Code の自己申告 (record の kind / msg)。無ければ省く。
	AgentKind     string     `json:"agent_kind,omitempty"`
	AgentMessage  string     `json:"agent_message,omitempty"`
	MonitorStatus string     `json:"monitor_status"`
	WaitUntil     *time.Time `json:"wait_until,omitempty"`
	Attempts      int        `json:"attempts"`
	LastOutcome   string     `json:"last_outcome"`
	LastTickAt    time.Time  `json:"last_tick_at"`
}

// StatusSnapshot は status.json (XDG state 配下、apppath.StatusFile) の内容。daemon が
// tick のたびに上書きし、`status` コマンドがこれを読んで表示する。
type StatusSnapshot struct {
	PID int `json:"pid"`
	// Backend は daemon が見ている多重化ツール (rex)。status / doctor が表示に使う。
	// 記録を始める前の daemon が書いたものでは空になる。
	Backend   string       `json:"backend,omitempty"`
	StartedAt time.Time    `json:"started_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Panes     []PaneStatus `json:"panes"`
}

// monitorStatusString は monitor.Status を status.json 向けの識別子文字列にする。
func monitorStatusString(s monitor.Status) string {
	if s == monitor.StatusWaiting {
		return "waiting"
	}
	return "monitoring"
}

// zeroableTime はゼロ値の time.Time を nil に変換する。PaneState.WaitUntil は
// 監視中 (未待機) の間ゼロ値のままなので、JSON へは省略して出す。
func zeroableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// writeStatusSnapshot は status.json を原子的に書き込む (statefile)。
// 人が cat して読む想定なので indent 付きで書く。
func writeStatusSnapshot(path string, pid int, backend string, startedAt, now time.Time, panes []PaneStatus) error {
	snap := StatusSnapshot{PID: pid, Backend: backend, StartedAt: startedAt, UpdatedAt: now, Panes: panes}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("status snapshot の JSON 化に失敗: %w", err)
	}
	return statefile.WriteFileAtomic(path, ".status.json.*", data)
}

// ReadStatusSnapshot は path から status.json を読む。ファイルが存在しない場合は
// (daemon が一度も tick していない、または起動していない) (nil, nil) を返す。
func ReadStatusSnapshot(path string) (*StatusSnapshot, error) {
	var snap StatusSnapshot
	found, err := statefile.ReadJSON(path, &snap)
	if err != nil || !found {
		return nil, err
	}
	return &snap, nil
}

// Running は動いている daemon の pid と、その daemon が書いた status.json を返す。daemon が動いていなければ pid は 0。
// status.json が無いか前の daemon が残したものなら snap は nil (snap.PID を pid と突き合わせる。突き合わせないと、
// 数秒前に起きた daemon の情報として古い pane の一覧や起動時刻を見せてしまう)。status / board / adopt が同じ判断で読む。
func Running(p apppath.Paths) (pid int, snap *StatusSnapshot, err error) {
	if pid, err = ReadPIDFile(p.PIDFile()); err != nil || !PIDAlive(pid) {
		return 0, nil, err
	}
	if snap, err = ReadStatusSnapshot(p.StatusFile()); err != nil {
		return 0, nil, err
	}
	if snap != nil && snap.PID != pid {
		snap = nil
	}
	return pid, snap, nil
}
