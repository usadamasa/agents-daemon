// Package limitlog は daemon が上限の出来事 (検知して待機に入った、送った、手を引いた、判定できなかった) を
// 踏んだ瞬間に、後から検証に要る材料を 1 出来事 1 行の JSON で残す。
//
//	limit-events/<YYYY-MM-DD>.jsonl
//
// daemon.log の行は「何をしたか」だけで、判定の根拠 (読んだ transcript の entry、record、待機明けに読んだ画面) は
// 残らない。上限は起こさないと測れず、次に踏んだときに手で見に行くには遅いので、daemon が踏んだ瞬間の材料をここへ写す。
// 読み方は .claude/skills/changing-agents-daemon-internals/references/verification.md「次に上限を踏んだとき」。
// 待機明けに読んだ画面 (上限の文言と通知欄) をそのまま含むので、監視中の pane に cat しない。
package limitlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/statefile"
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
	// Record は同じ tick の PaneList が返した Claude Code の自己申告。
	Record Record `json:"record"`
	// Transcript は判定の根拠にした transcript の末尾の entry。読めなければ無く、Reason に理由。
	Transcript *Transcript `json:"transcript,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	// Screen と Hint は待機明けに読んだ画面 (末尾 readLines 行) と、その通知欄の判定 (detect.Hint の String())。
	// 検知の tick では読まないので無い。
	Screen string `json:"screen,omitempty"`
	Hint   string `json:"hint,omitempty"`
	// Geometry は出来事の瞬間に打った size と modes。取れなければ GeometryError に理由。
	Geometry      *multiplexer.Geometry `json:"geometry,omitempty"`
	GeometryError string                `json:"geometry_error,omitempty"`
	// TurnFailure はその pane のセッションの最後の turn を終わらせた API error (turnfailure.Record の写し)。
	// 記録は次の turn で消えるので、あれば「最後の turn が API error で終わり、まだ次が始まっていない」。
	// transcript の判定と食い違えば、transcript の形が変わった疑い。
	TurnFailure *TurnFailure `json:"turn_failure,omitempty"`
	// Settings は判定に効いた設定値。
	Settings Settings `json:"settings"`
}

// Record は Claude Code の自己申告 (multiplexer.Pane の AgentStatus / AgentKind / AgentMessage)。
type Record struct {
	AgentStatus string `json:"agent_status"`
	Kind        string `json:"kind,omitempty"`
	Msg         string `json:"msg,omitempty"`
}

// Transcript は transcript の末尾の entry (transcript.Entry の写し) と、そのファイルのパス。
type Transcript struct {
	Path           string    `json:"path"`
	UUID           string    `json:"uuid"`
	Type           string    `json:"type"`
	Timestamp      time.Time `json:"timestamp"`
	Version        string    `json:"version,omitempty"`
	Error          string    `json:"error,omitempty"`
	APIErrorStatus int       `json:"api_error_status,omitempty"`
	Quota          *Quota    `json:"quota_limits,omitempty"`
	// Text は本文の頭 (textLimit 文字まで)。
	Text string `json:"text,omitempty"`
}

// Quota は entry の quotaLimits のうち読んだ欄。
type Quota struct {
	Status        string     `json:"status"`
	RateLimitType string     `json:"rate_limit_type,omitempty"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
	OverageStatus string     `json:"overage_status,omitempty"`
}

// TextLimit は Transcript.Text に残す本文の長さ (rune)。上限の文は 1 行で、会話の応答を丸ごと写す理由は無い。
const TextLimit = 200

// TurnFailure は StopFailure hook が受けた error。欄の名前は hook の入力に揃える。
type TurnFailure struct {
	Error        string    `json:"error"`
	ErrorDetails string    `json:"error_details,omitempty"`
	Message      string    `json:"last_assistant_message,omitempty"`
	ObservedAt   time.Time `json:"observed_at"`
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
	return statefile.PruneDir(s.Dir, now.Add(-Retention))
}
