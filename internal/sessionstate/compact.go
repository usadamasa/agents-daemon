package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"
)

// contextFile は context/<session_id>.json の生の形｡
type contextFile struct {
	SessionID      string  `json:"session_id"`
	UsedPercentage    float64 `json:"used_percentage"`
	ObservedAt        int64   `json:"observed_at"`
	ContextWindowSize int64   `json:"context_window_size,omitempty"`
	// TranscriptPath は CacheLoader が辿る｡Compact には載せない｡
	TranscriptPath string `json:"transcript_path,omitempty"`
}

// Compact はセッション 1 つぶんの､compact 自動化に要る状態をまとめたもの｡
// 4 つの state は互いに独立に現れるため､どれが無くても他は使える｡
type Compact struct {
	// HasContext は context 使用率が読めたかどうか｡
	HasContext bool
	// UsedPercentage は context の使用率 (0..100)｡
	UsedPercentage float64
	// ObservedAt は statusline がその使用率を観測した時刻｡
	ObservedAt time.Time
	// ContextWindowSize は statusline が渡す context window の大きさ (トークン数)｡記録が無ければ 0｡
	ContextWindowSize int64

	// HasPrep は compact-prep の state file が存在するかどうか｡
	HasPrep bool
	// PrepWrittenAt は state file の mtime｡「/compact-prep を送ったあとに
	// 書かれたか」を送信時刻との前後で判定するために使う｡
	PrepWrittenAt time.Time

	// HasCompacted は圧縮完了 marker が存在するかどうか｡
	HasCompacted bool
	// CompactedAt は marker の mtime (= 圧縮が完了した時刻)｡
	CompactedAt time.Time

	// HasIdleCompacted は idle compact の marker が存在するかどうか｡
	HasIdleCompacted bool
	// IdleCompactedAt は marker の mtime (= idle compact の /compact を送った時刻)｡
	IdleCompactedAt time.Time
}

// IdleCompactActive は idle compact の後で利用者がまだ戻っていないか､つまり marker があり
// cache が無いか marker 以前の応答しか持たないかを返す｡marker は消さずにこの比較で解く
// (skills/agents-daemon/references/compact.md の「idle compact」)｡
func (c *Compact) IdleCompactActive(cache *Cache) bool {
	if c == nil || !c.HasIdleCompacted {
		return false
	}
	return cache == nil || !cache.LastRequestAt.After(c.IdleCompactedAt)
}

// MarkIdleCompacted は cache-idle-compacted/<session_id> を書く (中身は空で､mtime が送信時刻)｡
// daemon が idle compact の /compact を送る直前に呼ぶ｡sessionID が空なら何もしない｡
func (s Store) MarkIdleCompacted(sessionID string) error {
	path, err := sessionPath(s.IdleCompacted, sessionID, "")
	if err != nil || path == "" {
		return err
	}
	return writeFileAtomic(path, ".idle-compacted.*", nil)
}

// CacheUsedPercentage は cache に記録された直近の応答の入力トークン数から context の使用率を
// 求める｡statusline の used_percentage と同じく整数へ丸める｡window の大きさかトークン数の
// 記録が無ければ false｡
//
// statusline の観測と違い､idle な間も値が古くならない (直近の応答から使用率は変わらない)｡
func (c *Compact) CacheUsedPercentage(cache *Cache) (float64, bool) {
	if c == nil || cache == nil || c.ContextWindowSize <= 0 || cache.ContextTokens <= 0 {
		return 0, false
	}
	return math.Round(float64(cache.ContextTokens) * 100 / float64(c.ContextWindowSize)), true
}

// ContextFresh は使用率の観測が maxAge 以内かを返す｡
// 古い観測で compact を起こさないためのゲート｡
func (c *Compact) ContextFresh(now time.Time, maxAge time.Duration) bool {
	if c == nil || !c.HasContext {
		return false
	}
	return fresh(c.ObservedAt, now, maxAge)
}

// LoadCompact は sessionID に対応する compact 関連の 4 つの state を読む｡
//
// sessionID が空 (herdr が pane に agent session を紐づけていない) 場合は
// 判断材料が無いという正常系として (nil, nil) を返す｡呼び出し元は nil を
// 「compact の自動化を適用しない」として扱う｡
//
// ファイルが無いのは正常系で､対応する Has* が false になるだけ｡JSON が
// 壊れている場合だけエラーを返す｡
func (s Store) LoadCompact(sessionID string) (*Compact, error) {
	contextPath, err := sessionPath(s.Context, sessionID, ".json")
	if err != nil || contextPath == "" {
		return nil, err
	}
	prepPath, err := sessionPath(s.CompactState, sessionID, ".md")
	if err != nil {
		return nil, err
	}
	markerPath, err := sessionPath(s.Compacted, sessionID, "")
	if err != nil {
		return nil, err
	}
	idlePath, err := sessionPath(s.IdleCompacted, sessionID, "")
	if err != nil {
		return nil, err
	}

	state := &Compact{}
	if err := loadContext(state, contextPath); err != nil {
		return nil, err
	}
	state.HasPrep, state.PrepWrittenAt = modTime(prepPath)
	state.HasCompacted, state.CompactedAt = modTime(markerPath)
	state.HasIdleCompacted, state.IdleCompactedAt = modTime(idlePath)
	return state, nil
}

// loadContext は context 使用率の JSON を state へ埋める｡
func loadContext(state *Compact, path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が session ID を検証済みのパスを渡す
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("context state の読み込みに失敗: %w", err)
	}
	var raw contextFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("context state のパースに失敗: %w", err)
	}
	state.HasContext = true
	state.UsedPercentage = raw.UsedPercentage
	state.ObservedAt = time.Unix(raw.ObservedAt, 0)
	state.ContextWindowSize = raw.ContextWindowSize
	return nil
}

// modTime は path の mtime を返す｡存在しなければ (false, ゼロ値)｡
// 中身を読まないのは､marker が空ファイルであり state file も本文を daemon が
// 使わないため｡存在と時刻だけが判定に効く｡
func modTime(path string) (bool, time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return false, time.Time{}
	}
	return true, info.ModTime()
}
