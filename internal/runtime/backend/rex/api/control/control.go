// Package control は rex server の control API (Unix socket の上の WebSocket、subprotocol rex.control.v1) の client。
// 接続 1 本の上で request と response を id で突き合わせ、server が求めずに送る event を queue へ渡す。
// どの method をどう呼ぶか、attach をいつ打つかは呼び手 (api) が持つ。プロトコルの原本は server の `/llms.txt`。
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// APIRevision は hello で名乗る API revision。server はこの revision の形へ訳して返すので、応答の形はこの値で決まる。
// rex-premise: preview.30 api control API は revision 14。hello の api_revision に合わせて server が形を訳す
const APIRevision = 14

// subprotocol は control 接続の WebSocket の subprotocol。
const subprotocol = "rex.control.v1"

// minMessageBytes は welcome が言う max_message_bytes の下限 (どの server も 64 KiB 以上を受ける)。
const minMessageBytes = 64 * 1024

// errClosed は Close の後の呼び出しの失敗。
var errClosed = errors.New("rex の control 接続は閉じている")

// apiError は server が response の error で返した失敗。分岐は code で行い、message は人向けの文面。
type apiError struct {
	method  string
	code    string
	message string
	status  int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("rex %s: %s (%s, status %d)", e.method, e.message, e.code, e.status)
}

// IsNotFound は err が対象 (block、window、session) が無いことによる失敗かを返す。
// 閉じた block や session を読み飛ばす判定に使う。
// rex-premise: preview.30 api 無い block は not_found、無い session は session_not_found (status 404)
func IsNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && (e.code == "not_found" || e.code == "session_not_found")
}

// Options は Dial の設定。
type Options struct {
	// ClientName と ClientVersion は hello の client に載せる (rex の client 一覧に出る)。
	ClientName    string
	ClientVersion string
}

// welcome は server が hello に返した welcome のうち、client が使うもの。
type welcome struct {
	apiRevision     int
	maxMessageBytes int
}

// Conn は control 接続 1 本。method は並んで呼べる。
type Conn struct {
	ws      *websocket.Conn
	welcome welcome
	nextID  atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan response
	err     error

	done   chan struct{}
	cancel context.CancelFunc
}

type response struct {
	payload json.RawMessage
	err     *apiError
}

// message は server が送る message の、種類を見分けるのに要る欄。
type message struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Seq      int64           `json:"seq"`
	Payload  json.RawMessage `json:"payload"`
	Error    *wireError      `json:"error"`
	Protocol string          `json:"protocol"`
	Limits   struct {
		MaxMessageBytes int `json:"max_message_bytes"`
	} `json:"limits"`
	APIRevision int `json:"api_revision"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// Dial は socket へ control 接続を張り、hello を送って welcome を確かめる。ctx は張るまでの期限で、
// 張った後の接続には効かない (閉じるのは Close)。
func Dial(ctx context.Context, socketPath string, opts Options) (*Conn, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	}}
	// Unix socket の上では URL の host 名は何でもよい。
	ws, _, err := websocket.Dial(ctx, "ws://localhost/api/control", &websocket.DialOptions{
		HTTPClient:   &http.Client{Transport: transport},
		Subprotocols: []string{subprotocol},
		// hello は reconnect token を載せうるので圧縮しない (docs)。使わない今も既定に任せず切っておく。
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("rex の control 接続 (%s) を張れない: %w", socketPath, err)
	}
	w, err := handshake(ctx, ws, opts)
	if err != nil {
		_ = ws.CloseNow()
		return nil, err
	}
	ws.SetReadLimit(int64(w.maxMessageBytes))

	readCtx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		ws:      ws,
		welcome: w,
		pending: map[string]chan response{},
		done:    make(chan struct{}),
		cancel:  cancel,
	}
	go c.readLoop(readCtx)
	return c, nil
}

// handshake は hello を送り、welcome の type と protocol を確かめる。
func handshake(ctx context.Context, ws *websocket.Conn, opts Options) (welcome, error) {
	hello, err := json.Marshal(map[string]any{
		"type":         "hello",
		"api_revision": APIRevision,
		"client":       map[string]any{"kind": "agent", "name": opts.ClientName, "version": opts.ClientVersion},
	})
	if err != nil {
		return welcome{}, err
	}
	if err := ws.Write(ctx, websocket.MessageText, hello); err != nil {
		return welcome{}, fmt.Errorf("rex へ hello を送れない: %w", err)
	}
	_, b, err := ws.Read(ctx)
	if err != nil {
		return welcome{}, fmt.Errorf("rex から welcome を受けられない: %w", err)
	}
	var m message
	if err := json.Unmarshal(b, &m); err != nil {
		return welcome{}, fmt.Errorf("rex の welcome を読めない: %w", err)
	}
	if m.Type != "welcome" || m.Protocol != subprotocol {
		return welcome{}, fmt.Errorf("rex の最初の message が %s の welcome でない (type=%q protocol=%q)", subprotocol, m.Type, m.Protocol)
	}
	return welcome{apiRevision: m.APIRevision, maxMessageBytes: max(m.Limits.MaxMessageBytes, minMessageBytes)}, nil
}

// Call は method を呼び、response の payload を out へ読む (out が nil なら読まない)。server の失敗は code を持つ
// error で返し、対象が無い失敗は IsNotFound で見分ける。
// ctx が終われば待つのをやめる (server は request を走らせ終えることがある)。
func (c *Conn) Call(ctx context.Context, method string, payload, out any) error {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("rex %s: %w", method, err)
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := map[string]any{"type": "request", "id": id, "method": method}
	if payload != nil {
		req["payload"] = payload
	}
	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("rex %s: request を組めない: %w", method, err)
	}
	// Write の ctx が途中で終わると接続ごと閉じる (ライブラリの仕様)。書き込みは数 ms で済み、
	// 書けないなら接続が壊れているので、呼び手の ctx をそのまま渡す。
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("rex %s: request を送れない: %w", method, err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			e := *r.err
			e.method = method
			return &e
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(r.payload, out); err != nil {
			return fmt.Errorf("rex %s: 応答の parse に失敗: %w (payload=%q)", method, err, r.payload)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("rex %s: %w", method, ctx.Err())
	case <-c.done:
		return fmt.Errorf("rex %s: 応答の前に接続が切れた: %w", method, c.Err())
	}
}

// Done は接続が終わったら閉じる。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err は接続が終わった理由。終わっていなければ nil。
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close は close frame を送って接続を閉じ、読む goroutine が終わるのを待つ。close frame で閉じた client は
// server がすぐ片付ける (frame 無しで切ると 60 秒残る)。close の握手の失敗 (server が先に切った、2 回目の Close) は
// 呼び手にできることが無いので返さない。
func (c *Conn) Close() error {
	c.fail(errClosed)
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
	c.cancel()
	<-c.done
	return nil
}

// fail は接続の終わった理由を最初の 1 回だけ覚える。
func (c *Conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

// readLoop は接続が終わるまで message を読み、response を待ち手へ渡す。event は読む口がまだ無いので捨てる。
// 読み続けることで server の ping にも答え、event の backlog で server に切られない。
func (c *Conn) readLoop(ctx context.Context) {
	defer func() {
		c.cancel()
		_ = c.ws.CloseNow()
		close(c.done)
	}()
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			c.fail(fmt.Errorf("rex の control 接続が切れた: %w", err))
			return
		}
		var m message
		if err := json.Unmarshal(b, &m); err != nil {
			continue // 読めない message は飛ばす (新しい server が足した形でありうる)
		}
		if m.Type == "response" {
			c.deliver(m)
		}
	}
}

func (c *Conn) deliver(m message) {
	c.mu.Lock()
	ch := c.pending[m.ID]
	c.mu.Unlock()
	if ch == nil {
		return // 待ち手が ctx で去った後の応答
	}
	r := response{payload: m.Payload}
	if m.Error != nil {
		r.err = &apiError{code: m.Error.Code, message: m.Error.Message, status: m.Error.Status}
	}
	ch <- r
}
