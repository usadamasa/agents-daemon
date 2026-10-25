// Package record は dream harvest の出力 ($STATE/dream/harvest/<YYYY-MM-DD>.jsonl) の書式と読み書きを持つ。
//
// 書くのは harvest、読むのは mine。両者が同じ型を使い、書式の写しを持たない。
package record

// Record は 1 セッション 1 レコードの集約結果。
// 出力 JSONL の各行はこの構造体を JSON 化したもの。
type Record struct {
	SessionID         string             `json:"session_id"`
	TSStart           string             `json:"ts_start,omitempty"`
	TSEnd             string             `json:"ts_end,omitempty"`
	CWD               string             `json:"cwd,omitempty"`
	CWDProject        string             `json:"cwd_project,omitempty"`
	NMessages         int                `json:"n_messages"`
	UserPrompts       []string           `json:"user_prompts"`
	SkillsInvoked     []string           `json:"skills_invoked"`
	FeedbackSignals   []FeedbackSignal   `json:"feedback_signals"`
	FailedInvocations []FailedInvocation `json:"failed_invocations"`
}

// FeedbackSignal は user の訂正・却下発言をキャプチャする。
type FeedbackSignal struct {
	Quote string `json:"quote"`
	Kind  string `json:"kind"` // "correction" | "stop_doing" | "rejection"
}

// FailedInvocation は「skill が invoked された直後に user が訂正した」ケースを記録する。
// Mine phase の skill_description trigger 拡張候補の evidence 源。
type FailedInvocation struct {
	InvokedSkill string `json:"invoked_skill"`
	UserReaction string `json:"user_reaction"` // 直後の user prompt (200 char truncate)
}
