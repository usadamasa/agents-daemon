// Package multiplexer は端末多重化ツールの pane 操作を表す中立の interface と型を持つ｡
// rex の backend (rexcli) がこれを実装し､backend 固有の JSON は backend に閉じる｡
package multiplexer

import (
	"context"
	"strings"
)

// AgentStatus は backend が pane に対して報告する agent の状態｡
// rex では Claude Code 自身が OSC 7501 で申告する state をそのまま写す｡
type AgentStatus string

const (
	// AgentStatusIdle は入力待ちで､かつそのタブが focused UI で一度観測された状態｡
	AgentStatusIdle AgentStatus = "idle"
	// AgentStatusWorking は作業中｡この状態の pane には絶対に割り込まない｡
	AgentStatusWorking AgentStatus = "working"
	// AgentStatusBlocked は承認待ち・質問待ちなど backend が UI から認識した状態｡
	AgentStatusBlocked AgentStatus = "blocked"
	// AgentStatusDone は idle と同じ実体だが､focused UI で未観測のままバックグラウンド作業が終わった状態｡
	AgentStatusDone AgentStatus = "done"
	// AgentStatusUnknown は agent は存在するが backend が分類できない状態｡完了の証明にはならない｡
	AgentStatusUnknown AgentStatus = "unknown"
)

// Pane は 1 pane 分の情報｡agent が乗っていない素の shell pane では Agent と SessionID が空になる｡
type Pane struct {
	PaneID string
	// TerminalID は daemon が pane ごとの状態を持つ key｡
	TerminalID  string
	CWD         string
	Agent       string
	AgentStatus AgentStatus
	// SessionID は pane で動いている Claude セッションの ID (CLAUDE_CODE_SESSION_ID)｡
	// pane と Claude セッションの唯一の対応付けで､紐づいていなければ空｡
	SessionID     string
	TerminalTitle string
}

// IsClaudeAgent は pane が Claude Code の agent を持つかを判定する｡
// agent フィールドへの大文字小文字を無視した部分一致で判定する｡
func IsClaudeAgent(p Pane) bool {
	return strings.Contains(strings.ToLower(p.Agent), "claude")
}

// PaneReadOptions は PaneRead の呼び出しオプション｡
type PaneReadOptions struct {
	Lines int // 0 以下なら行数を指定しない
}

// SessionResolver は pane (rex は block) の ID から Claude Code の session_id への対応を返す｡
// 実体は statusline が `context/<sid>.json` の multiplexer に書いた記録 (sessionstate.Store.ListMultiplexerBlocks)｡
// backend は PaneList / PaneGet の呼び出しごとに 1 回だけ呼ぶ｡
type SessionResolver func() (map[string]string, error)

// Client は多重化ツールが提供する pane 操作の抽象｡daemon はこの interface にのみ
// 依存し､exec.Command を直接呼ばない｡
type Client interface {
	// PaneList は現在の全 pane を返す｡
	PaneList(ctx context.Context) ([]Pane, error)
	// PaneGet は指定 pane の情報を返す｡
	PaneGet(ctx context.Context, paneID string) (Pane, error)
	// PaneRead は pane の画面テキストを返す｡
	PaneRead(ctx context.Context, paneID string, opts PaneReadOptions) (string, error)
	// SendText は pane へリテラルテキストを送る (Enter は押さない)｡
	SendText(ctx context.Context, paneID, text string) error
	// SendKeys は pane へキー入力を送る (例: "esc", "enter")｡
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	// Reachable は backend の server に到達できるかを確認する (doctor コマンド用)｡
	// 到達できなければ理由を含む error を返す｡
	Reachable(ctx context.Context) error
}
