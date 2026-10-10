// Package herdrcli は herdr CLI (https://herdr.dev) を叩く multiplexer.Client の実装を提供する｡
// herdr 形の JSON はこのパッケージの非公開の wire 型に閉じ､外へは multiplexer.Pane で渡す｡
package herdrcli

import "github.com/usadamasa/agents-daemon/internal/multiplexer"

// agentSessionWire は pane に紐づく agent セッションの識別情報｡
// Claude Code の pane では Value が CLAUDE_CODE_SESSION_ID と一致する｡
type agentSessionWire struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// paneWire は `herdr pane list` / `herdr pane get` が返す 1 pane 分の情報｡
// agent が乗っていない素の shell pane では Agent が空文字列､AgentSession が nil になる
// (実機の herdr 0.8.0 で確認済み)｡daemon が読まないフィールドは受けない｡
type paneWire struct {
	Agent         string            `json:"agent,omitempty"`
	AgentSession  *agentSessionWire `json:"agent_session,omitempty"`
	AgentStatus   string            `json:"agent_status"`
	CWD           string            `json:"cwd"`
	PaneID        string            `json:"pane_id"`
	TerminalID    string            `json:"terminal_id"`
	TerminalTitle string            `json:"terminal_title"`
}

// toPane は herdr の pane を中立の multiplexer.Pane へ写す｡agent_status は herdr の 5 値が
// そのまま multiplexer.AgentStatus の値になる｡
func (w paneWire) toPane() multiplexer.Pane {
	p := multiplexer.Pane{
		PaneID:        w.PaneID,
		TerminalID:    w.TerminalID,
		CWD:           w.CWD,
		Agent:         w.Agent,
		AgentStatus:   multiplexer.AgentStatus(w.AgentStatus),
		TerminalTitle: w.TerminalTitle,
	}
	if w.AgentSession != nil {
		p.SessionID = w.AgentSession.Value
	}
	return p
}
