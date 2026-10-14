// Package statusline は Claude Code の statusline コマンドの描画を持つ｡
// claude-config の dotclaude/statusline.sh (bash + jq) を移したもので､絵文字・色・閾値・
// 行構成はそのまま｡stdin の JSON の取り出し (Input)､幅モード (Mode)､描画 (View.Render) に
// 分かれ､state の読み書き (sessionstate) と git / settings の参照 (resolve.go) は
// 呼び出し側 (cmd) が組み立てて View に渡す｡
package statusline

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// Input は statusline の stdin から描画に要る値を取り出したもの｡
type Input struct {
	Model       string
	CurrentDir  string
	ProjectDir  string
	Worktree    string
	OutputStyle string
	Version     string
	// Cost はセッションの累計 (USD)｡
	Cost float64
	// ContextTokens と ContextPercent は Claude Code がモデルごとの実効上限を踏まえて
	// 算出済みの値｡こちらで再計算しない｡
	ContextTokens  int64
	ContextPercent int
	SessionID      string
	// 利用上限は「無い」を nil で表す (非サブスク契約では 3 つとも nil)｡
	FiveHourPercent  *int
	FiveHourResetsAt *time.Time
	SevenDayPercent  *int
}

// rawInput は stdin の JSON のうち描画に要る部分｡null と 0 を区別するものは pointer で受ける｡
type rawInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir  string `json:"current_dir"`
		ProjectDir  string `json:"project_dir"`
		GitWorktree string `json:"git_worktree"`
	} `json:"workspace"`
	Cwd         string `json:"cwd"`
	OutputStyle struct {
		Name string `json:"name"`
	} `json:"output_style"`
	Version string `json:"version"`
	Cost    struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		TotalInputTokens float64 `json:"total_input_tokens"`
		UsedPercentage   float64 `json:"used_percentage"`
	} `json:"context_window"`
	SessionID  string `json:"session_id"`
	RateLimits struct {
		FiveHour rawWindow `json:"five_hour"`
		SevenDay rawWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type rawWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

// ParseInput は stdin の JSON を Input にする｡無い値は statusline.sh の既定 (model は "?"､
// current_dir が無ければ cwd､数値は 0) に揃える｡
func ParseInput(data []byte) (Input, error) {
	var raw rawInput
	if err := json.Unmarshal(data, &raw); err != nil {
		return Input{}, fmt.Errorf("statusline の入力のパースに失敗: %w", err)
	}
	in := Input{
		Model:          raw.Model.DisplayName,
		CurrentDir:     raw.Workspace.CurrentDir,
		ProjectDir:     raw.Workspace.ProjectDir,
		Worktree:       raw.Workspace.GitWorktree,
		OutputStyle:    raw.OutputStyle.Name,
		Version:        raw.Version,
		Cost:           raw.Cost.TotalCostUSD,
		ContextTokens:  int64(raw.ContextWindow.TotalInputTokens),
		ContextPercent: roundPercent(raw.ContextWindow.UsedPercentage),
		SessionID:      raw.SessionID,
	}
	if in.Model == "" {
		in.Model = "?"
	}
	if in.CurrentDir == "" {
		in.CurrentDir = raw.Cwd
	}
	if p := raw.RateLimits.FiveHour.UsedPercentage; p != nil {
		in.FiveHourPercent = new(roundPercent(*p))
	}
	if at := raw.RateLimits.FiveHour.ResetsAt; at != nil {
		in.FiveHourResetsAt = new(time.Unix(int64(*at), 0))
	}
	if p := raw.RateLimits.SevenDay.UsedPercentage; p != nil {
		in.SevenDayPercent = new(roundPercent(*p))
	}
	return in, nil
}

// roundPercent は jq の round と同じ四捨五入 (0.5 は 0 から遠い側へ)｡
func roundPercent(v float64) int {
	return int(math.Round(v))
}

// cc-premise: 2.1.153 statusline 実行前に COLUMNS が設定される
//
// Mode は表示幅による段階的な圧縮｡Claude Code v2.1.153+ は statusline の実行前に COLUMNS へ
// 端末の実サイズを入れる｡herdr / rex で pane を分割すると幅が縮み､3 行構成のままでは
// 折り返して行数を食うため､幅に応じて出力量を落とす｡
type Mode int

const (
	// ModeFull は 3 行構成｡
	ModeFull Mode = iota
	// ModeCompact は 1 行に畳む (100 桁未満)｡
	ModeCompact
	// ModeHidden は何も出さない (60 桁未満)｡
	ModeHidden
)

const (
	compactColumns = 100
	hiddenColumns  = 60
)

// ModeFor は COLUMNS の値からモードを決める｡取れない環境 (古い版､他ハーネス) では
// 0 や非数値になり､従来どおり full にする｡
func ModeFor(columns string) Mode {
	cols, err := strconv.Atoi(columns)
	if err != nil || cols <= 0 {
		return ModeFull
	}
	switch {
	case cols < hiddenColumns:
		return ModeHidden
	case cols < compactColumns:
		return ModeCompact
	default:
		return ModeFull
	}
}
