package rexcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/cli"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control"
)

// clientName は hello で名乗る client の名前 (rex の client 一覧に出る)。
const clientName = "agents-daemon"

// terminalPrefix は terminal の block の method の接頭辞。block の method は `<creator>.<method>` で呼ぶ。
// rex-premise: preview.30 api block の method は com.superlogical.terminal.<名前> で、session_id と block_id と args を取る
const terminalPrefix = "com.superlogical.terminal."

// version は hello で名乗る版。go build のバイナリが持つ module の版で、取れなければ dev。
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

// errBlockNotFound は target.resolve が block を見つけられなかったことを表す。
var errBlockNotFound = errors.New("block not found")

// defaultSocket は環境から control API の socket のパスを決める。
func defaultSocket() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("rex の socket の既定の場所を決める home が分からない: %w", err)
	}
	return control.SocketPath(os.Getenv, home)
}

// conn は使える control 接続を返す。無いか切れていれば張り直し、session.attach_all まで済ませる。
// attach していない接続から session を名指す method は conflict で断られる。
// rex-premise: preview.30 api session.attach_all の後は全 session の method が通る (attach 前は conflict)
func (c *execClient) conn(ctx context.Context) (*control.Conn, error) {
	c.apiMu.Lock()
	defer c.apiMu.Unlock()
	if c.api != nil {
		select {
		case <-c.api.Done():
			c.api = nil
		default:
			return c.api, nil
		}
	}
	if c.socket == nil {
		return nil, errors.New("rex の control API の socket が決まっていない")
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
	c.api = conn
	return conn, nil
}

// call は control API の method を 1 回呼ぶ。timeout が 0 以下なら制限時間を課さない (テスト用)。
func (c *execClient) call(ctx context.Context, method string, payload, out any) error {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	conn, err := c.conn(ctx)
	if err != nil {
		return err
	}
	return conn.Call(ctx, method, payload, out)
}

// blockCall は terminal の block の method を呼ぶ。args が nil なら送らない。
func (c *execClient) blockCall(ctx context.Context, session, blockID, method string, args, out any) error {
	payload := map[string]any{"session_id": session, "block_id": blockID}
	if args != nil {
		payload["args"] = args
	}
	return c.call(ctx, terminalPrefix+method, payload, out)
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

// readFormat は format を打ち、続きがあれば (応答に id があれば) blob_read で終わりまで読み足す。
// 続きは format の結果を server が保つ 5 分の間に読む。
func (c *execClient) readFormat(ctx context.Context, session, blockID string, args map[string]any) (string, error) {
	var first formatResult
	if err := c.blockCall(ctx, session, blockID, "format", args, &first); err != nil {
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
		if err := c.blockCall(ctx, session, blockID, "blob_read", blob, &part); err != nil {
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

// blockSession は block の ID からそれを持つ session を引く。block の method は session_id が要る。
// 文脈を渡さないと全 session を探す (文脈の session を渡すとその中に閉じ、別 session の block が not_found になる)。
// rex-premise: preview.30 api target.resolve は context が無ければ全 session から block を引き、target.session_id を返す
func (c *execClient) blockSession(ctx context.Context, blockID string) (string, error) {
	var res resolveResult
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

// Close は control 接続を閉じる。短命のサブコマンドは終わる前に呼び、close frame で server に client を片付けさせる。
func (c *execClient) Close() error {
	c.apiMu.Lock()
	defer c.apiMu.Unlock()
	if c.api == nil {
		return nil
	}
	err := c.api.Close()
	c.api = nil
	return err
}

// isNotFound は対象 (block、session) が無いことによる失敗かを返す。CLI の exit 3 と API の not_found の両方を拾う。
func isNotFound(err error) bool {
	return cli.TargetNotFound(err) || control.IsNotFound(err) || errors.Is(err, errBlockNotFound)
}
