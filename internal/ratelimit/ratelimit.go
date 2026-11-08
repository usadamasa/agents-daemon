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

// Retention は利用上限 state を残す期間。resets_at は観測から高々 5 時間先で、
// LatestWindow は未来の resets_at しか使わないため、24 時間より古い state が判定に
// 効くことは無い。「この state がいつまで意味を持つか」は書式の知識なので、消す側
// (sessionstate.Prune) ではなくここに置く。
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
// 書き (Write) の両方がこの 1 つを使う。seven_day は daemon に読み手が
// 無いが、書き手が入力にあるときだけ付けるので pointer + omitempty で持つ。
// フィールドの順がそのままファイルの並びになる。
type rateLimitFile struct {
	FiveHour   windowFile  `json:"five_hour"`
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
// その場合は FiveHourResetsAt をゼロ値のままにして HasFiveHour で区別する。
type State struct {
	// SessionID はファイルの中身が名乗るセッション ID。
	SessionID string
	// ObservedAt は statusline がその値を観測した時刻。
	ObservedAt time.Time
	// FiveHourUsed は 5 時間ウィンドウの使用率 (0..100)。
	FiveHourUsed float64
	// FiveHourResetsAt は 5 時間ウィンドウのリセット時刻。値が無ければゼロ値。
	FiveHourResetsAt time.Time
}

// HasFiveHour は 5 時間ウィンドウの有効な値を持つかを返す。
func (r *State) HasFiveHour() bool {
	return r != nil && !r.FiveHourResetsAt.IsZero()
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

func (w Window) file() windowFile {
	return windowFile{UsedPercentage: w.UsedPercentage, ResetsAt: w.ResetsAt.Unix()}
}

// Write は sessionID の利用上限 state を書く。sevenDay は観測に無ければ nil。
// 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う (statefile)。
func (s Store) Write(sessionID string, observedAt time.Time, fiveHour Window, sevenDay *Window) error {
	path, err := s.file(sessionID)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("rate-limits の書き込みに session ID が無い")
	}
	out := rateLimitFile{
		FiveHour:   fiveHour.file(),
		ObservedAt: observedAt.Unix(),
		SessionID:  sessionID,
	}
	if sevenDay != nil {
		w := sevenDay.file()
		out.SevenDay = &w
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
		SessionID:    raw.SessionID,
		ObservedAt:   time.Unix(raw.ObservedAt, 0),
		FiveHourUsed: raw.FiveHour.UsedPercentage,
	}
	if raw.FiveHour.ResetsAt != 0 {
		state.FiveHourResetsAt = time.Unix(raw.FiveHour.ResetsAt, 0)
	}
	return state, nil
}
