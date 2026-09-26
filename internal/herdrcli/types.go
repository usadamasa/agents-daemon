// Package herdrcli は herdr CLI (https://herdr.dev) の薄いラッパーを提供する｡
// daemon はこのパッケージの Client interface にのみ依存し､herdr を直接 exec しない｡
package herdrcli

import "strings"

// AgentStatus は herdr が pane に対して報告する agent の状態｡
// herdr --skill が定義する 5 値のみが実際に返る｡
type AgentStatus string

const (
	// AgentStatusIdle は入力待ちで､かつそのタブが focused UI で一度観測された状態｡
	AgentStatusIdle AgentStatus = "idle"
	// AgentStatusWorking は作業中｡この状態の pane には絶対に割り込まない｡
	AgentStatusWorking AgentStatus = "working"
	// AgentStatusBlocked は承認待ち・質問待ちなど herdr が UI から認識した状態｡
	AgentStatusBlocked AgentStatus = "blocked"
	// AgentStatusDone は idle と同じ実体だが､focused UI で未観測のままバックグラウンド作業が終わった状態｡
	AgentStatusDone AgentStatus = "done"
	// AgentStatusUnknown は agent は存在するが herdr が分類できない状態｡完了の証明にはならない｡
	AgentStatusUnknown AgentStatus = "unknown"
)

// AgentSession は pane に紐づく agent セッションの識別情報｡
// Claude Code の pane では Value が CLAUDE_CODE_SESSION_ID と一致し､
// これが「どの Claude セッションがこの pane で動いているか」の唯一の対応付けになる｡
type AgentSession struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// scroll は pane のスクロール位置情報｡呼び出し元がこの型を名指しすることはなく
// (Pane.Scroll 経由でしか触らない)､公開する API 表面を増やすだけなので非公開にしてある｡
type scroll struct {
	MaxOffsetFromBottom int `json:"max_offset_from_bottom"`
	OffsetFromBottom    int `json:"offset_from_bottom"`
	ViewportRows        int `json:"viewport_rows"`
}

// Pane は `herdr pane list` / `herdr pane get` が返す 1 pane 分の情報｡
// agent が乗っていない素の shell pane では Agent が空文字列､AgentSession が nil になる
// (実機の herdr 0.8.0 で確認済み)｡
type Pane struct {
	Agent                 string        `json:"agent,omitempty"`
	AgentSession          *AgentSession `json:"agent_session,omitempty"`
	AgentStatus           AgentStatus   `json:"agent_status"`
	CWD                   string        `json:"cwd"`
	Focused               bool          `json:"focused"`
	ForegroundCWD         string        `json:"foreground_cwd"`
	PaneID                string        `json:"pane_id"`
	Revision              int           `json:"revision"`
	Scroll                scroll        `json:"scroll"`
	TabID                 string        `json:"tab_id"`
	TerminalID            string        `json:"terminal_id"`
	TerminalTitle         string        `json:"terminal_title"`
	TerminalTitleStripped string        `json:"terminal_title_stripped"`
	WorkspaceID           string        `json:"workspace_id"`
}

// IsClaudeAgent は pane が Claude Code の agent を持つかを判定する｡
// 参考実装 (herdr-claude-auto-retry の herdr.js, isClaudeAgent) にならい､
// agent フィールドへの大文字小文字を無視した部分一致で判定する｡
func IsClaudeAgent(p Pane) bool {
	return strings.Contains(strings.ToLower(p.Agent), "claude")
}

// SessionID は pane で動いている Claude セッションの ID を返す｡
// herdr が agent session を紐づけていない pane では空文字列を返す｡
func (p Pane) SessionID() string {
	if p.AgentSession == nil {
		return ""
	}
	return p.AgentSession.Value
}
