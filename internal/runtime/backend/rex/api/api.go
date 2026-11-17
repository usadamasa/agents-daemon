// Package api は rex server の control API の method を型付きで呼ぶ client。呼び手は method 名と payload の形を知らない。
// 接続は 1 本を最初の呼び出しで張って使い回し、切れていれば張り直す。block の method が要る session の解決や
// format の分割読みのように、複数の request を組む呼び方もここに置く。応答の型は wire、プロトコルは control が持つ。
package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// clientName は hello で名乗る client の名前 (rex の client 一覧に出る)。
const clientName = "agents-daemon"

// terminalPrefix は terminal の block の method の接頭辞。block の method は `<creator>.<method>` で呼ぶ。
// rex-premise: preview.30 api block の method は com.superlogical.terminal.<名前> で、session_id と block_id と args を取る
const terminalPrefix = "com.superlogical.terminal."

// errBlockNotFound は target.resolve が block を見つけられなかったことを表す。
var errBlockNotFound = errors.New("block not found")

// Client は control 接続を 1 本持ち、使い回す。method は並んで呼べる。
type Client struct {
	// socket は control API の socket のパスを返す。
	socket func() (string, error)
	// timeout は request 1 回の制限時間。0 以下なら課さない (テスト用)。
	timeout time.Duration
	// mu は conn を守る。conn が nil なら次の呼び出しで張る。
	mu   sync.Mutex
	conn *control.Conn
}

// New は socket の control API を呼ぶ Client を返す。接続は最初の呼び出しで張る。
func New(socket func() (string, error), timeout time.Duration) *Client {
	return &Client{socket: socket, timeout: timeout}
}

// DefaultSocket は環境から control API の socket のパスを決める (REX_SERVER か、macOS の既定の場所)。
func DefaultSocket() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("rex の socket の既定の場所を決める home が分からない: %w", err)
	}
	return control.SocketPath(os.Getenv, home)
}

// IsNotFound は対象 (block、session) が無いことによる失敗かを返す。server の not_found / session_not_found と、
// target.resolve が見つけられなかったことの両方を拾う。閉じた block や session を読み飛ばす判定に使う。
func IsNotFound(err error) bool {
	return control.IsNotFound(err) || errors.Is(err, errBlockNotFound)
}

// IsConflict は今の状態では通らない失敗 (閉じかけの session など) かを返す。
func IsConflict(err error) bool { return control.IsConflict(err) }

// Close は control 接続を閉じる。短命のサブコマンドは終わる前に呼び、close frame で server に client を片付けさせる。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// ServerStatus は server.status の応答のうち使う部分。
type ServerStatus struct {
	PID int `json:"pid"`
}

// ServerStatus は server.status を呼ぶ。
func (c *Client) ServerStatus(ctx context.Context) (ServerStatus, error) {
	var st ServerStatus
	err := c.call(ctx, "server.status", nil, &st)
	return st, err
}

// ListSessions は session.list を呼び、session の ID を作った順に返す。
func (c *Client) ListSessions(ctx context.Context) ([]string, error) {
	var res struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := c.call(ctx, "session.list", nil, &res); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		ids = append(ids, s.SessionID)
	}
	return ids, nil
}

// ListBlocks は session の block を flavor を問わず返す。
// rex-premise: preview.30 api session.list_blocks は session の全 window と detached の block を返し、無い session は session_not_found
func (c *Client) ListBlocks(ctx context.Context, session string) (wire.BlockList, error) {
	var blocks wire.BlockList
	err := c.call(ctx, "session.list_blocks", map[string]any{"session_id": session}, &blocks)
	return blocks, err
}

// ResolveBlock は block の ID からそれを持つ session を引く。block の method は session_id が要る。
// 文脈を渡さないと全 session を探す (文脈の session を渡すとその中に閉じ、別 session の block が not_found になる)。
// rex-premise: preview.30 api target.resolve は context が無ければ全 session から引き、target.session_id を返す
func (c *Client) ResolveBlock(ctx context.Context, blockID string) (string, error) {
	var res struct {
		Results []struct {
			Status string `json:"status"`
			Target struct {
				SessionID string `json:"session_id"`
			} `json:"target"`
			Reason string `json:"reason"`
		} `json:"results"`
	}
	if err := c.call(ctx, "target.resolve", map[string]any{"kinds": []string{"block"}, "queries": []string{blockID}}, &res); err != nil {
		return "", err
	}
	if len(res.Results) != 1 || res.Results[0].Status != "resolved" || res.Results[0].Target.SessionID == "" {
		reason := ""
		if len(res.Results) == 1 {
			reason = res.Results[0].Reason
		}
		return "", fmt.Errorf("rex の block %s: %w %s", blockID, errBlockNotFound, reason)
	}
	return res.Results[0].Target.SessionID, nil
}

// ProgramStatus は block の program_status (OSC 7501 の record) を呼ぶ。
// rex-premise: preview.30 api program_status と process の結果は CLI の block call と同じ形
func (c *Client) ProgramStatus(ctx context.Context, session, blockID string) (wire.ProgramStatus, error) {
	var ps wire.ProgramStatus
	err := c.blockCall(ctx, session, blockID, "program_status", nil, &ps)
	return ps, err
}

// Process は block の process (前景の process) を呼ぶ。
func (c *Client) Process(ctx context.Context, session, blockID string) (wire.Process, error) {
	var p wire.Process
	err := c.blockCall(ctx, session, blockID, "process", nil, &p)
	return p, err
}

// Size は block の端末の大きさを呼ぶ。
// rex-premise: preview.30 api size は rows と columns を返す
func (c *Client) Size(ctx context.Context, session, blockID string) (wire.Size, error) {
	var s wire.Size
	err := c.blockCall(ctx, session, blockID, "size", nil, &s)
	return s, err
}

// Modes は block の端末の mode を呼ぶ。modes は idle の terminal を起こさない。
// rex-premise: preview.30 api modes は screen (primary / alternate) を返す
func (c *Client) Modes(ctx context.Context, session, blockID string) (wire.Modes, error) {
	var m wire.Modes
	err := c.blockCall(ctx, session, blockID, "modes", nil, &m)
	return m, err
}

// FormatOptions は format の args。
type FormatOptions struct {
	// Trim は行末の空白を消す。Unwrap は端末が折り返した行を繋ぐ。VT は制御シーケンス (SGR、CR) を保つ。
	Trim, Unwrap, VT bool
}

// formatChunkBytes は format と blob_read の 1 回で受ける大きさ。server が受ける最大 (960 KiB) にし、
// 1 回の応答の上限 (1 MiB) に収める。max_bytes を付けない format は上限を超えると too_large で落ちる
// (scrollback は 64 MiB まで溜まる。2026-11-17 に 3 MB の scrollback で実測)。
// rex-premise: preview.30 api format は max_bytes を超える続きに id を付け、blob_read で offset から読ませる。max_bytes の上限は 983040
const formatChunkBytes = 983040

// Format は block の画面と scrollback を返す。alternate screen では可視の行だけになる。続きがあれば (応答に id があれば)
// blob_read で終わりまで読み足す。続きは format の結果を server が保つ 5 分の間に読む。
// rex-premise: preview.30 api format は alternate screen では可視の行だけ、通常の画面では scrollback も返す。trim の content は改行で終わらない
func (c *Client) Format(ctx context.Context, session, blockID string, opts FormatOptions) (string, error) {
	args := map[string]any{"trim": opts.Trim, "unwrap": opts.Unwrap, "max_bytes": formatChunkBytes}
	if opts.VT {
		args["format"] = "vt"
	}
	type chunk struct {
		Content string `json:"content"`
		Data    string `json:"data"`
		ID      int64  `json:"id"`
		Total   int    `json:"total"`
		EOF     bool   `json:"eof"`
	}
	var first chunk
	if err := c.blockCall(ctx, session, blockID, "format", args, &first); err != nil {
		return "", err
	}
	if first.ID == 0 {
		return first.Content, nil
	}
	var b strings.Builder
	b.WriteString(first.Content)
	// blob_read は続きが残っていても max_bytes より短く返しうるので、受けた分だけ offset を進め、total に届くまで読む。
	// 届く前に空の data が来たら、途中までの画面を返さずに失敗にする。
	// rex-premise: preview.30 api blob_read の data は max_bytes より短いことがあり、offset は受けた data のバイト数で進める
	for b.Len() < first.Total {
		var part chunk
		blob := map[string]any{"id": first.ID, "offset": b.Len(), "max_bytes": formatChunkBytes}
		if err := c.blockCall(ctx, session, blockID, "blob_read", blob, &part); err != nil {
			return "", err
		}
		if part.Data == "" {
			return "", fmt.Errorf("rex の blob_read が %d バイト目で空を返した (全体は %d バイト)", b.Len(), first.Total)
		}
		b.WriteString(part.Data)
	}
	return b.String(), nil
}

// version は hello で名乗る版。go build のバイナリが持つ module の版で、取れなければ dev。
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

// connect は使える control 接続を返す。無いか切れていれば張り直し、session.attach_all まで済ませる。
// attach していない接続から session を名指す method は conflict で断られる。
// rex-premise: preview.30 api session.attach_all の後は全 session の method が通る (attach 前は conflict)
func (c *Client) connect(ctx context.Context) (*control.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		select {
		case <-c.conn.Done():
			c.conn = nil
		default:
			return c.conn, nil
		}
	}
	path, err := c.socket()
	if err != nil {
		return nil, err
	}
	conn, err := control.Dial(ctx, path, control.Options{ClientName: clientName, ClientVersion: version()})
	if err != nil {
		return nil, err
	}
	// 呼び出しの接続は event を読まないので、block_event を含まない範囲に絞って event の量を抑える。
	if err := conn.Call(ctx, "session.attach_all", map[string]any{"event_scope": "session"}, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

// call は method を 1 回呼び、応答を out へ読む (out が nil なら読まない)。
func (c *Client) call(ctx context.Context, method string, payload, out any) error {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	conn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	return conn.Call(ctx, method, payload, out)
}

// blockCall は terminal の block の method を呼ぶ。args が nil なら送らない。
func (c *Client) blockCall(ctx context.Context, session, blockID, method string, args, out any) error {
	payload := map[string]any{"session_id": session, "block_id": blockID}
	if args != nil {
		payload["args"] = args
	}
	return c.call(ctx, terminalPrefix+method, payload, out)
}
