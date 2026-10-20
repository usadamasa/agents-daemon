// Package multiplexer は端末多重化ツールの pane 操作を表す interface と型を持つ。
//
// 実装は rex (rexcli) の 1 つだけだが、interface は意図して残している (#73)。daemon / monitor / verify が
// rex の CLI (exec.Command と rex 固有の JSON) に直接依存しないための境界であり、テストが
// multiplexerfake を差し込む口でもある。複数 backend を抽象する目的の層ではない。
package multiplexer

import (
	"context"
	"strconv"
	"strings"
)

// AgentStatus は pane の agent の状態。rex では Claude Code 自身が OSC 7501 で申告する state を
// そのまま写す (rex は画面や process 名から補わない)。
type AgentStatus string

const (
	// AgentStatusIdle は入力待ちで、かつそのタブが focused UI で一度観測された状態。
	AgentStatusIdle AgentStatus = "idle"
	// AgentStatusWorking は作業中。この状態の pane には絶対に割り込まない。
	AgentStatusWorking AgentStatus = "working"
	// AgentStatusBlocked は Claude Code が「利用者の操作待ち」と自己申告した状態。permission prompt、
	// 利用者への質問、認証失敗、その他の dialog が開いている間 (kind の対応は
	// skills/agents-daemon/references/program-status.md)。
	AgentStatusBlocked AgentStatus = "blocked"
	// AgentStatusDone は idle と同じ実体だが、focused UI で未観測のままバックグラウンド作業が終わった状態。
	AgentStatusDone AgentStatus = "done"
	// AgentStatusError は Claude Code のターンが API error (rate limit を含む) で終わった状態。
	// 上限到達時の自己申告がこれかは未実測で、daemon はログに残すだけで判定には使わない (#77)。
	AgentStatusError AgentStatus = "error"
	// AgentStatusUnknown は agent は存在するが backend が分類できない状態。完了の証明にはならない。
	AgentStatusUnknown AgentStatus = "unknown"
)

// Pane は 1 pane 分の情報。agent が乗っていない素の shell pane では Agent と SessionID が空になる。
type Pane struct {
	// PaneID は rex の block ID。daemon が pane ごとの状態を持つ key にもなる。
	PaneID      string
	CWD         string
	Agent       string
	AgentStatus AgentStatus
	// AgentKind は blocked の種類 (record の kind)。AgentMessage は record の msg (作業中の要約、prompt の内容、
	// error の理由)。どちらも Claude Code の自己申告で、無ければ空。status / doctor / ログに出し、
	// AgentKind は monitor が再開送信を見送る判断にも使う。
	AgentKind    string
	AgentMessage string
	// SessionID は pane で動いている Claude セッションの ID (CLAUDE_CODE_SESSION_ID)。
	// pane と Claude セッションの唯一の対応付けで、紐づいていなければ空。
	SessionID     string
	TerminalTitle string
	// UnknownReason は agent の record があるのに使えず AgentStatus が unknown になった理由。空なら該当しない。
	// rex の版が変わって record の形が崩れたときに判定が黙って止まらないよう、daemon はログに、
	// doctor は pane 一覧に出す。
	UnknownReason string
}

// RecordLabel は record の自己申告を 1 行にする (`agent_status=blocked kind=permission msg="approve …"`)。
// kind と msg は空なら出さない。daemon のログ、status、doctor、inspect が同じ形で出す。
func RecordLabel(p Pane) string {
	label := "agent_status=" + string(p.AgentStatus)
	if p.AgentKind != "" {
		label += " kind=" + p.AgentKind
	}
	if p.AgentMessage != "" {
		label += " msg=" + strconv.Quote(p.AgentMessage)
	}
	return label
}

// IsClaudeAgent は pane が Claude Code の agent を持つかを判定する。
// agent フィールドへの大文字小文字を無視した部分一致で判定する。
func IsClaudeAgent(p Pane) bool {
	return strings.Contains(strings.ToLower(p.Agent), "claude")
}

// PaneReadOptions は PaneRead の呼び出しオプション。
type PaneReadOptions struct {
	Lines int // 0 以下なら行数を指定しない
}

// Screen は端末が見せている画面。alternate は全画面のプログラム (Claude Code を含む) が切り替えた画面で、
// scrollback を持たない。
type Screen string

const (
	ScreenPrimary   Screen = "primary"
	ScreenAlternate Screen = "alternate"
)

// Geometry は pane の端末の大きさと、見せている画面。alternate screen では PaneRead が返せるのは
// Rows 行までで、上限の文言が画面から押し出されると検知は効かない (pane-io.md「読み取り元」)。
type Geometry struct {
	Rows    int    `json:"rows"`
	Columns int    `json:"columns"`
	Screen  Screen `json:"screen"`
}

// SessionResolver は pane (rex は block) の ID から Claude Code の session_id への対応を返す。
// 実体は statusline が `context/<sid>.json` の multiplexer に書いた記録 (sessioncontext.Store.ListMultiplexerBlocks)。
// backend は PaneList / PaneGet の呼び出しごとに 1 回だけ呼ぶ。
type SessionResolver func() (map[string]string, error)

// PremiseFinding は PremiseChecker の点検 1 件。OK が false なら前提が崩れている (doctor が ⚠ で出す)。
type PremiseFinding struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

// PremiseChecker は多重化ツールの CLI に置いている前提 (応答の欄、exit code、flag) を、自分の pane に固定の
// コマンドを打って点検する口。doctor だけが使い、daemon の判定には関わらない。Client とは別にしてあるのは、
// 判定の経路 (daemon / monitor) とそのテスト (multiplexerfake) に点検の口を見せないため。実装は rexcli。
type PremiseChecker interface {
	// CheckPremises は paneID (自分の pane) と sessionID (その pane の session) を使って点検し、
	// 1 点検 1 件で返す。点検の失敗は error でなく OK=false の finding にする。
	CheckPremises(ctx context.Context, paneID, sessionID string) []PremiseFinding
}

// Client は多重化ツールが提供する pane 操作。実装は rexcli.NewExecClient と multiplexerfake.Client の
// 2 つで、daemon はこの interface にのみ依存し、exec.Command を直接呼ばない。
type Client interface {
	// PaneList は現在の全 pane を返す。
	PaneList(ctx context.Context) ([]Pane, error)
	// PaneGet は指定 pane の情報を返す。
	PaneGet(ctx context.Context, paneID string) (Pane, error)
	// PaneRead は pane の画面テキストを返す。
	PaneRead(ctx context.Context, paneID string, opts PaneReadOptions) (string, error)
	// PaneGeometry は pane の端末の大きさと画面を返す。毎 tick ではなく、上限の出来事の記録と doctor だけが呼ぶ。
	PaneGeometry(ctx context.Context, paneID string) (Geometry, error)
	// SendText は pane へリテラルテキストを送る (Enter は押さない)。
	SendText(ctx context.Context, paneID, text string) error
	// SendKeys は pane へキー入力を送る (例: "esc", "enter")。
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	// Reachable は backend の server に到達できるかを確認する (doctor コマンド用)。
	// 到達できなければ理由を含む error を返す。
	Reachable(ctx context.Context) error
}
