package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// contextFile は context/<session_id>.json の生の形。
type contextFile struct {
	SessionID      string  `json:"session_id"`
	UsedPercentage float64 `json:"used_percentage"`
	ObservedAt     int64   `json:"observed_at"`
}

// Compact はセッション 1 つぶんの、compact 自動化に要る状態をまとめたもの。
// 3 つの state は互いに独立に現れるため、どれが無くても他は使える。
type Compact struct {
	// HasContext は context 使用率が読めたかどうか。
	HasContext bool
	// UsedPercentage は context の使用率 (0..100)。
	UsedPercentage float64
	// ObservedAt は statusline がその使用率を観測した時刻。
	ObservedAt time.Time

	// HasPrep は compact-prep の state file が存在するかどうか。
	HasPrep bool
	// PrepWrittenAt は state file の mtime。「/compact-prep を送ったあとに
	// 書かれたか」を送信時刻との前後で判定するために使う。
	PrepWrittenAt time.Time

	// HasCompacted は圧縮完了 marker が存在するかどうか。
	HasCompacted bool
	// CompactedAt は marker の mtime (= 圧縮が完了した時刻)。
	CompactedAt time.Time
}

// ContextFresh は使用率の観測が maxAge 以内かを返す。
// 古い観測で compact を起こさないためのゲート。
func (c *Compact) ContextFresh(now time.Time, maxAge time.Duration) bool {
	if c == nil || !c.HasContext {
		return false
	}
	return fresh(c.ObservedAt, now, maxAge)
}

// LoadCompact は sessionID に対応する compact 関連の 3 つの state を読む。
//
// sessionID が空 (herdr が pane に agent session を紐づけていない) 場合は
// 判断材料が無いという正常系として (nil, nil) を返す。呼び出し元は nil を
// 「compact の自動化を適用しない」として扱う。
//
// ファイルが無いのは正常系で、対応する Has* が false になるだけ。JSON が
// 壊れている場合だけエラーを返す。
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

	state := &Compact{}
	if err := loadContext(state, contextPath); err != nil {
		return nil, err
	}
	state.HasPrep, state.PrepWrittenAt = modTime(prepPath)
	state.HasCompacted, state.CompactedAt = modTime(markerPath)
	return state, nil
}

// loadContext は context 使用率の JSON を state へ埋める。
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
	return nil
}

// modTime は path の mtime を返す。存在しなければ (false, ゼロ値)。
// 中身を読まないのは、marker が空ファイルであり state file も本文を daemon が
// 使わないため。存在と時刻だけが判定に効く。
func modTime(path string) (bool, time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return false, time.Time{}
	}
	return true, info.ModTime()
}
