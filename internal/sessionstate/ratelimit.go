package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// windowFile は 1 つの利用上限ウィンドウの生の形｡
type windowFile struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // Unix epoch 秒
}

// rateLimitFile は rate-limits/<session_id>.json の生の形｡読み (readRateLimit) と
// 書き (IngestStatusline) の両方がこの 1 つを使う｡seven_day は daemon に読み手が
// 無いが､書き手が入力にあるときだけ付けるので pointer + omitempty で持つ｡
// フィールドの順がそのままファイルの並びになる｡
type rateLimitFile struct {
	FiveHour   windowFile  `json:"five_hour"`
	SevenDay   *windowFile `json:"seven_day,omitempty"`
	ObservedAt int64       `json:"observed_at"`
	SessionID  string      `json:"session_id"`
}

// RateLimit は 1 セッションぶんの利用上限の状態｡
//
// 5 時間ウィンドウが切り替わる瞬間､Claude Code は statusline に
// five_hour: {used_percentage: null, resets_at: null} を渡す (実機で観測)｡
// encoding/json は null をゼロ値に読むため､そのままでは「使用率 0% / resets_at が
// epoch 0」と区別が付かない｡resets_at が 0 のウィンドウは実在しないので､
// その場合は FiveHourResetsAt をゼロ値のままにして HasFiveHour で区別する｡
type RateLimit struct {
	// SessionID はファイルの中身が名乗るセッション ID｡
	SessionID string
	// ObservedAt は statusline がその値を観測した時刻｡
	ObservedAt time.Time
	// FiveHourUsed は 5 時間ウィンドウの使用率 (0..100)｡
	FiveHourUsed float64
	// FiveHourResetsAt は 5 時間ウィンドウのリセット時刻｡値が無ければゼロ値｡
	FiveHourResetsAt time.Time
}

// IsFresh は観測が maxAge 以内かを返す｡
// statusline は画面に変化が無いと再描画を止めるため､古い state を検出できる必要がある｡
func (r *RateLimit) IsFresh(now time.Time, maxAge time.Duration) bool {
	if r == nil {
		return false
	}
	return fresh(r.ObservedAt, now, maxAge)
}

// HasFiveHour は 5 時間ウィンドウの有効な値を持つかを返す｡
func (r *RateLimit) HasFiveHour() bool {
	return r != nil && !r.FiveHourResetsAt.IsZero()
}

// FiveHourExhausted は 5 時間ウィンドウの使用率が threshold 以上かを返す｡
// 「上限に達していそうか」の判定に使う｡
func (r *RateLimit) FiveHourExhausted(threshold float64) bool {
	return r != nil && r.FiveHourUsed >= threshold
}

// RateLimitFile は sessionID に対応する利用上限 state のパスを返す｡
// sessionID が空､または不正な文字を含む場合はエラーを返す｡
func (s Store) RateLimitFile(sessionID string) (string, error) {
	return sessionPath(s.RateLimits, sessionID, ".json")
}

// LoadRateLimit は sessionID に対応する利用上限 state を読み込む｡
//
// sessionID が空 (herdr が pane に agent session を紐づけていない) 場合は､
// 判断材料が無いという正常系として (nil, nil) を返す｡呼び出し元は nil の state を
// 「ゲートを適用しない」として扱う｡ファイルが無い場合も同じく (nil, nil)｡
func (s Store) LoadRateLimit(sessionID string) (*RateLimit, error) {
	path, err := s.RateLimitFile(sessionID)
	if err != nil || path == "" {
		return nil, err
	}
	return readRateLimit(path)
}

// readRateLimit は path から利用上限 state を読み込む｡
// ファイルが存在しない場合は正常系として (nil, nil)､JSON が壊れている場合は
// それと区別してエラーを返す｡
func readRateLimit(path string) (*RateLimit, error) {
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
	state := &RateLimit{
		SessionID:    raw.SessionID,
		ObservedAt:   time.Unix(raw.ObservedAt, 0),
		FiveHourUsed: raw.FiveHour.UsedPercentage,
	}
	if raw.FiveHour.ResetsAt != 0 {
		state.FiveHourResetsAt = time.Unix(raw.FiveHour.ResetsAt, 0)
	}
	return state, nil
}

// maxResetAhead は LatestWindow が候補として受け付ける resets_at の上限 (now からの距離)｡
// 5 時間ウィンドウの解除が 5 時間より先になることは無い｡他セッションの state まで
// 信用する以上､1 ファイルのゴミが全 pane の起床時刻を汚さないよう､あり得ない値は弾く｡
const maxResetAhead = 6 * time.Hour

// LatestWindow は全セッションの state のうち､5 時間ウィンドウの resets_at が
// 最大のものを返す (同値なら observed_at が新しい方)｡
//
// resets_at はアカウント全体で共通の値で､同じウィンドウ内に観測された全セッションが
// 同じ resets_at を持つ (rate-limits/ 配下の実ファイルで確認済み)｡だで自分の state を
// 持たない pane (上限中に開いた新規セッション､429 で弾かれた turn は state を書かない)
// でも､他セッションの state から現在のウィンドウの解除時刻を引ける｡
//
// 壊れたファイル､five_hour が null のファイル､now+maxResetAhead より先の resets_at を
// 持つファイルは飛ばす｡1 ファイルの不良で走査全体を失敗させない｡ディレクトリが無い､
// または有効なファイルが無ければ (nil, nil) を返す｡
func (s Store) LatestWindow(now time.Time) (*RateLimit, error) {
	entries, err := os.ReadDir(s.RateLimits)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("rate-limits ディレクトリの走査に失敗: %w", err)
	}

	limit := now.Add(maxResetAhead)
	var best *RateLimit
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		state, loadErr := readRateLimit(filepath.Join(s.RateLimits, entry.Name()))
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

// newerWindow は a が b より新しいウィンドウ (resets_at が後､同値なら観測が後) かを返す｡
func newerWindow(a, b *RateLimit) bool {
	if !a.FiveHourResetsAt.Equal(b.FiveHourResetsAt) {
		return a.FiveHourResetsAt.After(b.FiveHourResetsAt)
	}
	return a.ObservedAt.After(b.ObservedAt)
}
