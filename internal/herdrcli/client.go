package herdrcli

import "context"

// PaneReadSource は `herdr pane read --source` に渡す値。
// herdr pane read --help で列挙される 4 値のみが有効 (実測確認済み)。
type PaneReadSource string

const (
	SourceVisible         PaneReadSource = "visible"
	SourceRecent          PaneReadSource = "recent"
	SourceRecentUnwrapped PaneReadSource = "recent-unwrapped"
	SourceDetection       PaneReadSource = "detection"
)

// PaneReadOptions は `herdr pane read` の呼び出しオプション。
type PaneReadOptions struct {
	Source PaneReadSource // 空文字列なら herdr のデフォルト (recent) に委ねる
	Lines  int            // 0 以下なら --lines を渡さない
}

// ReportMetadataOptions は `herdr pane report-metadata` の呼び出しオプション。
//
// Source は herdr がこのメタデータの出所を区別するための必須識別子 (--source)。
// StateLabels のキーは AgentStatus の値 (idle/working/blocked/done/unknown) に
// 限定される。これは herdr 0.8.0 の実機で確認した制約で、それ以外のキーを渡すと
// herdr 自身が "unknown state label: <key>" で拒否する。
//
// herdr 0.8.0 は --title / --clear-title / --display-agent / --clear-display-agent も
// サポートするが、agents-daemon はラベル (state-label) と TTL しか使わないため、
// この struct には含めていない。使う必要が生じたら実測して足す。
type ReportMetadataOptions struct {
	Source           string
	Agent            string
	StateLabels      map[AgentStatus]string
	ClearStateLabels bool
	TTLMillis        int // 0 以下なら --ttl-ms を渡さない
}

// Client は herdr CLI が提供する pane 操作の抽象。daemon はこの interface にのみ
// 依存し、exec.Command を直接呼ばない。
type Client interface {
	// PaneList は現在の全 pane を返す。
	PaneList(ctx context.Context) ([]Pane, error)
	// PaneGet は指定 pane の情報を返す。
	PaneGet(ctx context.Context, paneID string) (Pane, error)
	// PaneRead は pane の画面テキストを返す。応答は JSON envelope ではなく生テキスト。
	PaneRead(ctx context.Context, paneID string, opts PaneReadOptions) (string, error)
	// SendText は pane へリテラルテキストを送る (Enter は押さない)。
	SendText(ctx context.Context, paneID, text string) error
	// SendKeys は pane へキー入力を送る (例: "esc", "enter")。
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	// ReportMetadata は pane に表示用メタデータ (待機ラベル等) を添える。
	ReportMetadata(ctx context.Context, paneID string, opts ReportMetadataOptions) error
	// Reachable は herdr server に到達できるかを確認する (doctor コマンド用)。
	// 到達できなければ理由を含む error を返す。
	Reachable(ctx context.Context) error
}
