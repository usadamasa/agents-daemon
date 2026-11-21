// Package ratelimit は rate-limits/<session_id>.json (statusline が観測した利用上限) の
// 書式を読み書き両方で持つ。書くのは statusline の stdin を受ける sessionstate.IngestStatusline、
// 読むのは doctor (statusline が配線されているかの確認) だけで、daemon の判定には使わない。解除時刻は transcript の
// entry から取る。
//
// JSON の生の形 (rateLimitFile) と、それを読む関数は非公開にしてある。外から要るのは
// domain の型 (State) と、その解釈だけだったため。生の形を公開すると epoch 秒の
// time.Unix 変換が呼び出し側へ散る (実際に verify で 3 箇所に散っていた)。
package ratelimit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention は利用上限 state を残す期間。daemon は判定に読まず、doctor が statusline の配線の確認に
// 出すだけなので、24 時間更新の無い state は配線の確認にも役立たない。「この state がいつまで意味を持つか」は
// 書式の知識なので、消す側 (sessionstate.Prune) ではなくここに置く。
const Retention = 24 * time.Hour

// Store は rate-limits/ の置き場所を持つ。ゼロ値の Store は Dir が空文字列で、
// 読み込みは「まだ何も無い」として振る舞う (state を持たない呼び出し元のテストが
// そのまま書けるようにするため)。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "rate-limits")}
}

// windowFile は 1 つの利用上限ウィンドウの生の形。
type windowFile struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // Unix epoch 秒
}

// rateLimitFile は rate-limits/<session_id>.json の生の形。読み (readState) と
// 書き (Write) の両方がこの 1 つを使う。ウィンドウはどちらも入力にあるときだけ付けるので
// pointer + omitempty で持つ (5 時間ウィンドウの無いプランでは seven_day だけになる)。
// フィールドの順がそのままファイルの並びになる。
type rateLimitFile struct {
	FiveHour   *windowFile `json:"five_hour,omitempty"`
	SevenDay   *windowFile `json:"seven_day,omitempty"`
	ObservedAt int64       `json:"observed_at"`
	SessionID  string      `json:"session_id"`
}

// State は 1 セッションぶんの利用上限の状態。
//
// 5 時間ウィンドウが切り替わる瞬間、Claude Code は statusline に
// five_hour: {used_percentage: null, resets_at: null} を渡す (実機で観測)。
// encoding/json は null をゼロ値に読むため、そのままでは「使用率 0% / resets_at が
// epoch 0」と区別が付かない。resets_at が 0 のウィンドウは実在しないので、
// その場合はリセット時刻をゼロ値のままにして HasFiveHour / HasSevenDay で区別する。
type State struct {
	// SessionID はファイルの中身が名乗るセッション ID。
	SessionID string
	// ObservedAt は statusline がその値を観測した時刻。
	ObservedAt time.Time
	// FiveHourUsed は 5 時間ウィンドウの使用率 (0..100)。
	FiveHourUsed float64
	// FiveHourResetsAt は 5 時間ウィンドウのリセット時刻。値が無ければゼロ値。
	FiveHourResetsAt time.Time
	// SevenDayUsed は 7 日ウィンドウの使用率 (0..100)。
	SevenDayUsed float64
	// SevenDayResetsAt は 7 日ウィンドウのリセット時刻。値が無ければゼロ値。
	SevenDayResetsAt time.Time
}

// HasFiveHour は 5 時間ウィンドウの有効な値を持つかを返す。
func (r *State) HasFiveHour() bool {
	return r != nil && !r.FiveHourResetsAt.IsZero()
}

// HasSevenDay は 7 日ウィンドウの有効な値を持つかを返す。
func (r *State) HasSevenDay() bool {
	return r != nil && !r.SevenDayResetsAt.IsZero()
}

// file は sessionID に対応する利用上限 state のパスを返す。
// sessionID が空なら ("", nil)、不正な文字を含む場合はエラーを返す。
func (s Store) file(sessionID string) (string, error) {
	return statefile.SessionPath(s.Dir, sessionID, ".json")
}

// Window は書き込む 1 つの利用上限ウィンドウ。
type Window struct {
	UsedPercentage float64
	ResetsAt       time.Time
}

// file は w を書き出す形へ写す。w が nil なら nil。
func (w *Window) file() *windowFile {
	if w == nil {
		return nil
	}
	return &windowFile{UsedPercentage: w.UsedPercentage, ResetsAt: w.ResetsAt.Unix()}
}

// Write は sessionID の利用上限 state を書く。fiveHour と sevenDay は観測に無ければ nil で、
// 少なくとも片方が要る (どちらも無い state は statusline の配線の確認にも使えない)。
// 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う (statefile)。
func (s Store) Write(sessionID string, observedAt time.Time, fiveHour, sevenDay *Window) error {
	path, err := s.file(sessionID)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("rate-limits の書き込みに session ID が無い")
	}
	if fiveHour == nil && sevenDay == nil {
		return errors.New("rate-limits の書き込みに利用上限のウィンドウが 1 つも無い")
	}
	out := rateLimitFile{
		FiveHour:   fiveHour.file(),
		SevenDay:   sevenDay.file(),
		ObservedAt: observedAt.Unix(),
		SessionID:  sessionID,
	}
	return statefile.WriteJSONAtomic(path, ".rate-limits.*", out)
}

// Load は sessionID に対応する利用上限 state を読み込む。
//
// sessionID が空 (pane に session が紐づいていない。statusline が書く multiplexer の記録が無い) 場合は、
// 判断材料が無いという正常系として (nil, nil) を返す。呼び出し元は nil の state を
// 「ゲートを適用しない」として扱う。ファイルが無い場合も同じく (nil, nil)。
func (s Store) Load(sessionID string) (*State, error) {
	path, err := s.file(sessionID)
	if err != nil || path == "" {
		return nil, err
	}
	return readState(path)
}

// readState は path から利用上限 state を読み込む。
// ファイルが存在しない場合は正常系として (nil, nil)、JSON が壊れている場合は
// それと区別してエラーを返す。
func readState(path string) (*State, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が session ID を検証済みのパスを渡す
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("rate-limits.json の読み込みに失敗: %w", err)
	}

	var raw rateLimitFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("rate-limits.json のパースに失敗: %w", err)
	}
	state := &State{
		SessionID:  raw.SessionID,
		ObservedAt: time.Unix(raw.ObservedAt, 0),
	}
	state.FiveHourUsed, state.FiveHourResetsAt = raw.FiveHour.read()
	state.SevenDayUsed, state.SevenDayResetsAt = raw.SevenDay.read()
	return state, nil
}

// read はウィンドウの使用率とリセット時刻を返す。w が nil か resets_at が 0 (null) ならリセット時刻はゼロ値。
func (w *windowFile) read() (used float64, resetsAt time.Time) {
	if w == nil {
		return 0, time.Time{}
	}
	if w.ResetsAt != 0 {
		resetsAt = time.Unix(w.ResetsAt, 0)
	}
	return w.UsedPercentage, resetsAt
}
