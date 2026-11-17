package api

import (
	"context"
	"encoding/json"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control"
)

// Subscription は event を受けるためだけの control 接続 1 本。切れても張り直さない (張り直した接続は seq が 1 から
// 数え直しで、その間の event は失われる)。切れたら呼び手が Subscribe し直し、状態を読み直す。
type Subscription struct {
	conn *control.Conn
}

// Subscribe は呼び出しと別の接続を張り、session.attach_all (event_scope all) で server と全 session の event を
// 受け始める。queue は buffer 件で、溢れたら接続を閉じる。attach_all の応答より後の event だけが届く。
// rex-premise: preview.30 api event_scope all の接続にだけ block_event が届く。payload は rex events --all --json の 1 行と同じ形
func (c *Client) Subscribe(ctx context.Context, buffer int) (*Subscription, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	path, err := c.socket()
	if err != nil {
		return nil, err
	}
	conn, err := control.Dial(ctx, path, control.Options{ClientName: clientName, ClientVersion: version(), EventBuffer: buffer})
	if err != nil {
		return nil, err
	}
	if err := conn.Call(ctx, "session.attach_all", map[string]any{"event_scope": "all"}, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Subscription{conn: conn}, nil
}

// Events は event (`{"type": <名前>, "payload": <欄>}`) を届いた順に渡す。接続が終わると閉じる。
func (s *Subscription) Events() <-chan json.RawMessage { return s.conn.Events() }

// Err は接続が終わった理由。終わっていなければ nil。
func (s *Subscription) Err() error { return s.conn.Err() }

// Close は close frame を送って接続を閉じる。server は購読の client をすぐ片付ける。
func (s *Subscription) Close() error { return s.conn.Close() }
