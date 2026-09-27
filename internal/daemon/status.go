package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// PaneStatus は status.json に書き出す pane 1 枚分のスナップショット｡
type PaneStatus struct {
	PaneID        string     `json:"pane_id"`
	TerminalID    string     `json:"terminal_id"`
	CWD           string     `json:"cwd"`
	AgentStatus   string     `json:"agent_status"`
	MonitorStatus string     `json:"monitor_status"`
	WaitUntil     *time.Time `json:"wait_until,omitempty"`
	Attempts      int        `json:"attempts"`
	LastOutcome   string     `json:"last_outcome"`
	LastTickAt    time.Time  `json:"last_tick_at"`
	// Cache はそのセッションの prompt cache の状態｡sidecar (cache/<session_id>.json) が
	// 無ければ省略する｡
	Cache *CacheStatus `json:"cache,omitempty"`
}

// CacheStatus は status.json に書き出す prompt cache の状態｡State は tick 時点の判定で､
// 失効までの残りは読む側が ExpiresAt から出す｡
type CacheStatus struct {
	State         string    `json:"state"` // warm / expired
	LastRequestAt time.Time `json:"last_request_at"`
	TTLSeconds    int64     `json:"ttl_seconds"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// cacheStatus は sidecar の状態を status.json 向けに写す｡nil は nil のまま｡
func cacheStatus(c *sessionstate.Cache, now time.Time) *CacheStatus {
	if c == nil {
		return nil
	}
	state := "warm"
	if c.Expired(now) {
		state = "expired"
	}
	return &CacheStatus{
		State:         state,
		LastRequestAt: c.LastRequestAt,
		TTLSeconds:    int64(c.TTL / time.Second),
		ExpiresAt:     c.ExpiresAt(),
	}
}

// StatusSnapshot は status.json (XDG state 配下､apppath.StatusFile) の内容｡daemon が
// tick のたびに上書きし､`status` コマンドがこれを読んで表示する｡
type StatusSnapshot struct {
	PID       int          `json:"pid"`
	StartedAt time.Time    `json:"started_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Panes     []PaneStatus `json:"panes"`
}

// monitorStatusString は monitor.Status を status.json 向けの識別子文字列にする｡
func monitorStatusString(s monitor.Status) string {
	if s == monitor.StatusWaiting {
		return "waiting"
	}
	return "monitoring"
}

// zeroableTime はゼロ値の time.Time を nil に変換する｡PaneState.WaitUntil は
// 監視中 (未待機) の間ゼロ値のままなので､JSON へは省略して出す｡
func zeroableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// WriteStatusSnapshot は status.json を原子的に書き込む (同ディレクトリの
// 一時ファイルに書いて rename)｡
func writeStatusSnapshot(path string, pid int, startedAt, now time.Time, panes []PaneStatus) error {
	snap := StatusSnapshot{PID: pid, StartedAt: startedAt, UpdatedAt: now, Panes: panes}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("status snapshot の JSON 化に失敗: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("状態ディレクトリの作成に失敗: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".status.json.*")
	if err != nil {
		return fmt.Errorf("status snapshot の一時ファイル作成に失敗: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("status snapshot の書き込みに失敗: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("status snapshot のクローズに失敗: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("status snapshot の rename に失敗: %w", err)
	}
	return nil
}

// ReadStatusSnapshot は path から status.json を読む｡ファイルが存在しない場合は
// (daemon が一度も tick していない､または起動していない) (nil, nil) を返す｡
func ReadStatusSnapshot(path string) (*StatusSnapshot, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が固定パスを渡す
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("status snapshot の読み込みに失敗: %w", err)
	}
	var snap StatusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("status snapshot のパースに失敗: %w", err)
	}
	return &snap, nil
}
