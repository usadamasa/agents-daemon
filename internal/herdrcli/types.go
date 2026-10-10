// Package herdrcli は herdr (https://herdr.dev) を叩く client を提供する｡daemon が使う CLI 経由の
// multiplexer.Client (execClient) と､hook が使う socket 直叩きの SocketClient の 2 つで､応答の envelope の形を共有する｡
// herdr 形の JSON はこのパッケージの非公開の wire 型に閉じ､外へは multiplexer.Pane で渡す｡
package herdrcli

import "github.com/usadamasa/agents-daemon/internal/multiplexer"

// paneWire は `herdr pane list` / `herdr pane get` が返す 1 pane 分の情報｡
// agent が乗っていない素の shell pane では Agent が空文字列になる (実機の herdr 0.8.0 で確認済み)｡
// daemon が読まないフィールドは受けない｡pane の agent_session (herdr の integration script が入れる
// session_id) も受けない｡session_id は statusline が書いた sidecar から引く｡
type paneWire struct {
	Agent         string `json:"agent,omitempty"`
	AgentStatus   string `json:"agent_status"`
	CWD           string `json:"cwd"`
	PaneID        string `json:"pane_id"`
	TerminalID    string `json:"terminal_id"`
	TerminalTitle string `json:"terminal_title"`
}

// toPane は herdr の pane を中立の multiplexer.Pane へ写す｡agent_status は herdr の 5 値が
// そのまま multiplexer.AgentStatus の値になる｡sessionID は呼び出し側が sidecar から引いた値 (無ければ空)｡
func (w paneWire) toPane(sessionID string) multiplexer.Pane {
	return multiplexer.Pane{
		PaneID:        w.PaneID,
		TerminalID:    w.TerminalID,
		CWD:           w.CWD,
		Agent:         w.Agent,
		AgentStatus:   multiplexer.AgentStatus(w.AgentStatus),
		SessionID:     sessionID,
		TerminalTitle: w.TerminalTitle,
	}
}
