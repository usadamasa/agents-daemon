// Package api は rex server の control API を、使い回す接続 1 本の上で呼ぶ層。接続を初回の呼び出しで張り、切れていれば
// 張り直す。block の method が要る session の解決、format の分割読み、作った block の owner の手放しのように、
// 複数の request を組む呼び方もここに置く。multiplexer の形への写像は rexcli が持つ。
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

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control"
)

// clientName は hello で名乗る client の名前 (rex の client 一覧に出る)。
const clientName = "agents-daemon"

// terminalPrefix は terminal の block の method の接頭辞。block の method は `<creator>.<method>` で呼ぶ。
// rex-premise: preview.30 api block の method は com.superlogical.terminal.<名前> で、session_id と block_id と args を取る
const terminalPrefix = "com.superlogical.terminal."

// errBlockNotFound と errSessionNotFound は target.resolve が対象を見つけられなかったことを表す。
var (
	errBlockNotFound   = errors.New("block not found")
	errSessionNotFound = errors.New("session not found")
)

// Client は control 接続を 1 本持ち、使い回す。method は並んで呼べる。
type Client struct {
	// socket は control API の socket のパスを返す。
	socket func() (string, error)
	// timeout は request 1 回の制限時間。0 以下なら課さない (テスト用)。
	timeout time.Duration
	// mu は conn を守る。conn は nil なら次の呼び出しで張る。
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
	// 購読はまだ CLI の events なので、block_event を含まない範囲に絞って event の量を抑える。
	if err := conn.Call(ctx, "session.attach_all", map[string]any{"event_scope": "session"}, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

// Call は method を 1 回呼び、応答を out へ読む (out が nil なら読まない)。
func (c *Client) Call(ctx context.Context, method string, payload, out any) error {
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

// BlockCall は terminal の block の method を呼ぶ。args が nil なら送らない。
func (c *Client) BlockCall(ctx context.Context, session, blockID, method string, args, out any) error {
	payload := map[string]any{"session_id": session, "block_id": blockID}
	if args != nil {
		payload["args"] = args
	}
	return c.Call(ctx, terminalPrefix+method, payload, out)
}

// formatChunkBytes は format と blob_read の 1 回で受ける大きさ。server が受ける最大 (960 KiB) にし、
// 1 回の応答の上限 (1 MiB) に収める。max_bytes を付けない format は上限を超えると too_large で落ちる
// (scrollback は 64 MiB まで溜まる)。
// rex-premise: preview.30 api format は max_bytes を超える続きに id を付け、blob_read で offset から読ませる。max_bytes の上限は 983040
const formatChunkBytes = 983040

// formatResult は format と blob_read の応答。format は content、blob_read は data に本文を入れる。
type formatResult struct {
	Content string `json:"content"`
	Data    string `json:"data"`
	ID      int64  `json:"id"`
	EOF     bool   `json:"eof"`
}

// ReadFormat は args (trim、unwrap、format) で format を打ち、続きがあれば (応答に id があれば) blob_read で終わりまで
// 読み足す。1 回の大きさ (max_bytes) はここで決める。続きは format の結果を server が保つ 5 分の間に読む。
func (c *Client) ReadFormat(ctx context.Context, session, blockID string, args map[string]any) (string, error) {
	args["max_bytes"] = formatChunkBytes
	var first formatResult
	if err := c.BlockCall(ctx, session, blockID, "format", args, &first); err != nil {
		return "", err
	}
	if first.ID == 0 {
		return first.Content, nil
	}
	var b strings.Builder
	b.WriteString(first.Content)
	for {
		var part formatResult
		blob := map[string]any{"id": first.ID, "offset": b.Len(), "max_bytes": formatChunkBytes}
		if err := c.BlockCall(ctx, session, blockID, "blob_read", blob, &part); err != nil {
			return "", err
		}
		b.WriteString(part.Data)
		if part.EOF || part.Data == "" {
			return b.String(), nil
		}
	}
}

// resolveResult は target.resolve の応答のうち使う欄。
type resolveResult struct {
	Results []struct {
		Status string `json:"status"`
		Target struct {
			SessionID string `json:"session_id"`
		} `json:"target"`
		Reason string `json:"reason"`
	} `json:"results"`
}

// resolveSession は target.resolve で kind の query 1 つを引き、それを持つ (session なら自身の) session の ID を返す。
// 文脈を渡さないと全 session を探す (文脈の session を渡すとその中に閉じ、別 session の block が not_found になる)。
// rex-premise: preview.30 api target.resolve は context が無ければ全 session から引き、target.session_id を返す
func (c *Client) resolveSession(ctx context.Context, kind, query string, notFound error) (string, error) {
	var res resolveResult
	if err := c.Call(ctx, "target.resolve", map[string]any{"kinds": []string{kind}, "queries": []string{query}}, &res); err != nil {
		return "", err
	}
	if len(res.Results) != 1 || res.Results[0].Status != "resolved" || res.Results[0].Target.SessionID == "" {
		reason := ""
		if len(res.Results) == 1 {
			reason = res.Results[0].Reason
		}
		return "", fmt.Errorf("rex の %s %q: %w %s", kind, query, notFound, reason)
	}
	return res.Results[0].Target.SessionID, nil
}

// BlockSession は block の ID からそれを持つ session を引く。block の method は session_id が要る。
func (c *Client) BlockSession(ctx context.Context, blockID string) (string, error) {
	return c.resolveSession(ctx, "block", blockID, errBlockNotFound)
}

// SessionByName は session の ID か label から session の ID を引く (CLI の -s と同じく label でも名指せる)。
func (c *Client) SessionByName(ctx context.Context, name string) (string, error) {
	return c.resolveSession(ctx, "session", name, errSessionNotFound)
}

// ShellLayout は terminal の block 1 つの layout。options は flavor の options_schema の欄 (cwd、command、shell、login、exit)。
// rex-premise: preview.30 api com.superlogical.terminal.shell の options は cwd、command、shell、login、exit を取る。command は shell を通しても 1 引数ずつ子へ渡る
func ShellLayout(label string, options map[string]any) map[string]any {
	block := map[string]any{"flavor": blockstate.ShellFlavor, "options": options}
	if label != "" {
		block["label"] = label
	}
	return map[string]any{"block": block}
}

// Release は作った block を、作ったこの接続の持ち物から外す。block を作った client は owner になり、owner がいる間
// terminal は owner の大きさを使う。CLI が作った block は owner を持たないので揃える。張ったままの接続 (attach_all) で
// 作った block は接続を閉じても owner が残る (2026-11-17 に実測)。disown は session.attach した接続でしか通らない。
// 手放せなくても block は動いているので、呼び手は失敗を作成の失敗に数えない。
// rex-premise: preview.30 api 作った client が block の owner になる。disown_block は session.attach の後だけ通る
func (c *Client) Release(ctx context.Context, session string, blockIDs []string) error {
	if err := c.Call(ctx, "session.attach", map[string]any{"session_id": session}, nil); err != nil {
		return err
	}
	return c.Call(ctx, "session.disown_block", map[string]any{"session_id": session, "block_ids": blockIDs}, nil)
}

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

// IsNotFound は対象 (block、session) が無いことによる失敗かを返す。server の not_found と、target.resolve が
// 見つけられなかったことの両方を拾う。
func IsNotFound(err error) bool {
	return control.IsNotFound(err) || errors.Is(err, errBlockNotFound) || errors.Is(err, errSessionNotFound)
}
