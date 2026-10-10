package herdrcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// defaultSocketTimeout は ctx に期限が無いときの socket 1 往復の上限｡hook から呼ばれる前提なので短い｡
const defaultSocketTimeout = 4 * time.Second

// SocketClient は herdr の API socket (`HERDR_SOCKET_PATH`) を直接叩く｡
//
// hook は Claude Code の PATH を継承し､非対話で起動されたセッションでは homebrew のパスが載らないため､
// hook からは CLI (`herdr`) を当てにできない｡socket path は herdr が管理する pane に必ず注入するので､
// hook (herdr-title) はこちらを使う｡daemon が CLI を使い続けるのは､detach 後の長い稼働で socket path が
// 古くなりうるため (execClient.Reachable のコメント)｡
//
// protocol は改行区切りの JSON 1 行 (`{"id","method","params"}`) で､応答は envelope と同じ形｡
type SocketClient struct {
	path string
	// dial は 1 request ごとの接続を作る｡テストは net.Pipe で差し替える (sandbox は unix socket の bind を許さない)｡
	dial func(ctx context.Context) (net.Conn, error)
}

// NewSocketClient は path の unix socket を叩く client を返す｡
func NewSocketClient(path string) *SocketClient {
	return &SocketClient{
		path: path,
		dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}
}

// PaneView は `pane.current` の応答のうち herdr-title が使うもの｡
type PaneView struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	// TerminalTitleStripped は端末タイトルから herdr が装飾を除いたもの｡Claude Code はセッションの要約を置く｡
	TerminalTitleStripped string `json:"terminal_title_stripped"`
	// CWD は pane を作った場所｡ForegroundCWD は前面プロセスの cwd で､worktree へ移ったセッションはこちらが動く｡
	CWD           string `json:"cwd"`
	ForegroundCWD string `json:"foreground_cwd"`
}

// TabView は `tab.get` の応答のうち herdr-title が使うもの｡Label が空のタブは Number が表示される｡
type TabView struct {
	TabID  string `json:"tab_id"`
	Label  string `json:"label"`
	Number int    `json:"number"`
}

// PaneCurrent は callerPaneID から見た現在の pane を返す｡
// caller_pane_id を渡さないと focused pane が返り､別のタブを書き換える｡herdr は古い pane ID も今の pane へ解決する｡
func (c *SocketClient) PaneCurrent(ctx context.Context, callerPaneID string) (PaneView, error) {
	var res struct {
		Pane PaneView `json:"pane"`
	}
	if err := c.call(ctx, "pane.current", map[string]any{"caller_pane_id": callerPaneID}, &res); err != nil {
		return PaneView{}, err
	}
	return res.Pane, nil
}

// ReportWorkspaceMetadata は workspace の表示用 token を source 名義で報告する｡
// tokens の空の値は null で送り､その token を消す (空文字列だと空の行が残る)｡
// seq は同じ source の報告の順序で､大きいものが勝つ｡
func (c *SocketClient) ReportWorkspaceMetadata(ctx context.Context, workspaceID, source string, seq int64, tokens map[string]string) error {
	wire := make(map[string]any, len(tokens))
	for name, value := range tokens {
		if value == "" {
			wire[name] = nil
			continue
		}
		wire[name] = value
	}
	params := map[string]any{"workspace_id": workspaceID, "source": source, "seq": seq, "tokens": wire}
	return c.call(ctx, "workspace.report_metadata", params, nil)
}

// TabGet は tab の label と番号を返す｡
func (c *SocketClient) TabGet(ctx context.Context, tabID string) (TabView, error) {
	var res struct {
		Tab TabView `json:"tab"`
	}
	if err := c.call(ctx, "tab.get", map[string]any{"tab_id": tabID}, &res); err != nil {
		return TabView{}, err
	}
	return res.Tab, nil
}

// TabRename は tab の label を置き換える｡空文字列は連番ではなく空の label になるので､呼び出し側が番号を渡す｡
func (c *SocketClient) TabRename(ctx context.Context, tabID, label string) error {
	return c.call(ctx, "tab.rename", map[string]any{"tab_id": tabID, "label": label}, nil)
}

// call は 1 接続で request を 1 本送り､応答の envelope の result を out へ写す (out が nil なら捨てる)｡
func (c *SocketClient) call(ctx context.Context, method string, params map[string]any, out any) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultSocketTimeout)
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("herdr socket %s: %w", c.path, err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("herdr socket %s: deadline の設定に失敗: %w", c.path, err)
	}

	req := map[string]any{"id": "agents-daemon:" + method, "method": method, "params": params}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("herdr %s: request の encode に失敗: %w", method, err)
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("herdr %s: 送信に失敗: %w", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("herdr %s: 応答の受信に失敗: %w", method, err)
	}
	env, err := parseEnvelope(line, nil)
	if err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("herdr %s: result の parse に失敗: %w", method, err)
	}
	return nil
}
