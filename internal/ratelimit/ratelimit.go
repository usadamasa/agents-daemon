// Package ratelimit は rate-limits/<session_id>.json (statusline が観測した利用上限) の
// 書式を読み書き両方で持つ。書くのは statusline の stdin を受ける sessionstate.IngestStatusline、
// 読むのは daemon と診断 (verify、simulate)。
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

// IsFresh は観測が maxAge 以内かを返す。
// statusline は画面に変化が無いと再描画を止めるため、古い state を検出できる必要がある。
func (r *State) IsFresh(now time.Time, maxAge time.Duration) bool {
	if r == nil {
		return false
	}
	return fresh(r.ObservedAt, now, maxAge)
}

// HasFiveHour は 5 時間ウィンドウの有効な値を持つかを返す。
func (r *State) HasFiveHour() bool {
	return r != nil && !r.FiveHourResetsAt.IsZero()
}

// FiveHourExhausted は 5 時間ウィンドウの使用率が threshold 以上かを返す。
// 「上限に達していそうか」の判定に使う。
func (r *State) FiveHourExhausted(threshold float64) bool {
	return r != nil && r.FiveHourUsed >= threshold
}

// fresh は観測時刻 observedAt が now から maxAge 以内かを返す。
// observedAt が now より未来 (クロックスキュー等) の場合は信用せず false を返す。
func fresh(observedAt, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(observedAt)
	return age >= 0 && age <= maxAge
}

// File は sessionID に対応する利用上限 state のパスを返す。
// sessionID が空なら ("", nil)、不正な文字を含む場合はエラーを返す。
func (s Store) File(sessionID string) (string, error) {
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
	path, err := s.File(sessionID)
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
	path, err := s.File(sessionID)
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

// maxResetAhead は LatestWindow が候補として受け付ける resets_at の上限 (now からの距離)。
// 5 時間ウィンドウの解除が 5 時間より先になることは無い。他セッションの state まで
// 信用する以上、1 ファイルのゴミが全 pane の起床時刻を汚さないよう、あり得ない値は弾く。
const maxResetAhead = 6 * time.Hour

// LatestWindow は全セッションの state のうち、5 時間ウィンドウの resets_at が
// 最大のものを返す (同値なら observed_at が新しい方)。
//
// resets_at はアカウント全体で共通の値で、同じウィンドウ内に観測された全セッションが
// 同じ resets_at を持つ (rate-limits/ 配下の実ファイルで確認済み)。だで自分の state を
// 持たない pane (上限中に開いた新規セッション、429 で弾かれた turn は state を書かない)
// でも、他セッションの state から現在のウィンドウの解除時刻を引ける。
//
// 壊れたファイル、five_hour が null のファイル、now+maxResetAhead より先の resets_at を
// 持つファイルは飛ばす。1 ファイルの不良で走査全体を失敗させない。ディレクトリが無い、
// または有効なファイルが無ければ (nil, nil) を返す。
func (s Store) LatestWindow(now time.Time) (*State, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("rate-limits ディレクトリの走査に失敗: %w", err)
	}

	limit := now.Add(maxResetAhead)
	var best *State
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		state, loadErr := readState(filepath.Join(s.Dir, entry.Name()))
		if loadErr != nil || !state.HasFiveHour() {
			continue
		}
		if state.FiveHourResetsAt.After(limit) {
			continue
		}
		if best == nil || newerWindow(state, best) {
			best = state
		}
	}
	return best, nil
}

// newerWindow は a が b より新しいウィンドウ (resets_at が後、同値なら観測が後) かを返す。
func newerWindow(a, b *State) bool {
	if !a.FiveHourResetsAt.Equal(b.FiveHourResetsAt) {
		return a.FiveHourResetsAt.After(b.FiveHourResetsAt)
	}
	return a.ObservedAt.After(b.ObservedAt)
}
