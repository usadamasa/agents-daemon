// Package limitlog は daemon が上限の出来事 (検知して待機に入った、送った、抑制した、ネイティブに譲った) を
// 踏んだ瞬間に、後から検証に要る材料を 1 出来事 1 行の JSON で残す。
//
//	limit-events/<YYYY-MM-DD>.jsonl
//
// daemon.log の行は「何をしたか」だけで、判定した画面そのものと record と block の高さは残らない。上限は
// 起こさないと測れず、次に踏んだときに手で見に行くには遅いので、daemon が踏んだ瞬間の材料をここへ写す。
// 読み方と、どの欄が #39 / #77 のどの問いに答えるかは skills/agents-daemon/references/operations.md
// 「次に上限を踏んだとき」。画面の文面 (上限の文言と reset 時刻) をそのまま含むので、監視中の pane に
// cat しない (daemon がそれを limit と判定する)。
package limitlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// Retention は出来事の記録を残す期間。上限は週に数回で、確かめるのは次に rex や Claude Code の版を
// 追従するときなので 1 か月あれば足りる。画面の文面を含むので dream の 90 日より短くする。
const Retention = 30 * 24 * time.Hour

// dayLayout はファイル名の日付の書式 (daemon.log の rotate と同じ)。
const dayLayout = "2006-01-02"

// Store は limit-events/ の置き場所を持つ。ゼロ値 (Dir が空) は書かず、消さない。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "limit-events")}
}

// Event は 1 つの出来事。欄の無い値 (取れなかった geometry、無い state) は書かず、読む側が
// 「欄が無い = 取れなかった」と読めるようにする。
type Event struct {
	At        time.Time `json:"at"`
	PaneID    string    `json:"pane_id"`
	SessionID string    `json:"session_id,omitempty"`
	CWD       string    `json:"cwd"`
	// Outcome は monitor.Outcome の識別子、Kind は detect.Kind の識別子 (どちらも String())。
	Outcome  string `json:"outcome"`
	Kind     string `json:"kind"`
	Attempts int    `json:"attempts"`
	// WaitUntil は待機中の起床時刻。待機していなければ無い。
	WaitUntil *time.Time `json:"wait_until,omitempty"`
	// Record は同じ tick の PaneList が返した Claude Code の自己申告 (#77 の実測になる)。
	Record Record `json:"record"`
	// Geometry は出来事の瞬間に打った size と modes。取れなければ GeometryError に理由。
	Geometry      *multiplexer.Geometry `json:"geometry,omitempty"`
	GeometryError string                `json:"geometry_error,omitempty"`
	// RateLimit はその pane のセッションの state (own)、Account はアカウント全体で最新のウィンドウ。
	// ゲートと起床時刻の判断を再現するために残す。
	RateLimit *Window `json:"rate_limit,omitempty"`
	Account   *Window `json:"account,omitempty"`
	// Settings は判定に効いた設定値。
	Settings Settings `json:"settings"`
	// Screen は分類した画面そのもの (PaneRead が返した末尾 readLines 行)。`inspect --file` に渡せる。
	Screen string `json:"screen"`
}

// Record は Claude Code の自己申告 (multiplexer.Pane の AgentStatus / AgentKind / AgentMessage)。
type Record struct {
	AgentStatus string `json:"agent_status"`
	Kind        string `json:"kind,omitempty"`
	Msg         string `json:"msg,omitempty"`
}

// Window は利用上限 state の 5 時間ウィンドウ (ratelimit.State の写し)。
type Window struct {
	UsedPercentage float64   `json:"used_percentage"`
	ResetsAt       time.Time `json:"resets_at"`
	ObservedAt     time.Time `json:"observed_at"`
	SessionID      string    `json:"session_id,omitempty"`
}

// Settings は判定に効いた config の値。
type Settings struct {
	ReadLines          int `json:"read_lines"`
	DetectionTailLines int `json:"detection_tail_lines"`
}

// Append は ev を At の日付のファイルへ 1 行追記する。O_APPEND と 1 回の write で書くので、
// 同じファイルを別のプロセスが追記しても行は混ざらない。
func (s Store) Append(ev Event) error {
	if s.Dir == "" {
		return nil
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("上限の出来事の JSON 化に失敗: %w", err)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("%s の作成に失敗: %w", s.Dir, err)
	}
	path := filepath.Join(s.Dir, ev.At.Format(dayLayout)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- 固定パターンから組み立てたパス
	if err != nil {
		return fmt.Errorf("%s を開けない: %w", path, err)
	}
	_, writeErr := f.Write(append(data, '\n'))
	return errors.Join(writeErr, f.Close())
}

// Prune は mtime が Retention より古いファイルを消し、消した数を返す。ディレクトリが無ければ何もしない。
func (s Store) Prune(now time.Time) (int, error) {
	if s.Dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("%s の走査に失敗: %w", s.Dir, err)
	}
	cutoff := now.Add(-Retention)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("%s の情報を取得できません: %w", entry.Name(), err)
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir, entry.Name())); err != nil {
			return removed, fmt.Errorf("%s の削除に失敗: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}
