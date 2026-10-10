// Package multiplexer は端末多重化ツールの pane 操作を表す中立の interface と型を持つ｡
// herdr と rex の backend がこれを実装し､backend 固有の JSON は各 backend に閉じる｡
package multiplexer

import (
	"context"
	"strings"
)

// AgentStatus は backend が pane に対して報告する agent の状態｡
// 値の意味は herdr のもので､他の backend は同名の値へ写す｡
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
// 参考実装 (herdr-claude-auto-retry の herdr.js, isClaudeAgent) にならい､
// agent フィールドへの大文字小文字を無視した部分一致で判定する｡
func IsClaudeAgent(p Pane) bool {
	return strings.Contains(strings.ToLower(p.Agent), "claude")
}

// PaneReadSource は PaneRead が読む画面の範囲｡値は herdr の `pane read --source` の 4 値｡
type PaneReadSource string

const (
	SourceVisible         PaneReadSource = "visible"
	SourceRecent          PaneReadSource = "recent"
	SourceRecentUnwrapped PaneReadSource = "recent-unwrapped"
	SourceDetection       PaneReadSource = "detection"
)

// PaneReadOptions は PaneRead の呼び出しオプション｡
type PaneReadOptions struct {
	Source PaneReadSource // 空文字列なら backend のデフォルトに委ねる
	Lines  int            // 0 以下なら行数を指定しない
}

// ReportMetadataOptions は ReportMetadata の呼び出しオプション｡
//
// Source はメタデータの出所を区別するための必須識別子｡
// StateLabels は agent の状態ごとに pane へ添える表示ラベル｡
type ReportMetadataOptions struct {
	Source           string
	Agent            string
	StateLabels      map[AgentStatus]string
	ClearStateLabels bool
	TTLMillis        int // 0 以下なら TTL を指定しない
}

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
	// ReportMetadata は pane に表示用メタデータ (待機ラベル等) を添える｡
	ReportMetadata(ctx context.Context, paneID string, opts ReportMetadataOptions) error
	// Reachable は backend の server に到達できるかを確認する (doctor コマンド用)｡
	// 到達できなければ理由を含む error を返す｡
	Reachable(ctx context.Context) error
}
