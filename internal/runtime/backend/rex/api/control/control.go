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

// ErrNotSent は request を server へ送れなかったこと (送る前に接続が終わっていた、書き込みに失敗した)。server は
// request を受けていないので、冪等でない method でも別の接続で打ち直せる。送れた後に応答を待てなかった失敗は含まない。
var ErrNotSent = errors.New("request を送れない")

// errEventOverflow は event の queue が溢れて接続を閉じたこと。読み手が追いつかないまま event を捨てると、
// 欠けた event で状態を進めることになる。
var errEventOverflow = errors.New("rex の event の queue が溢れた")

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

// IsConflict は err が今の状態では通らない失敗 (session が閉じかけ、layout が動く間に block が閉じた、attach していない)
// かを返す。
// rex-premise: preview.30 api 閉じかけの session と、layout の変更中に閉じた block は conflict (status 409)
func IsConflict(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.code == "conflict"
}

// Options は Dial の設定。
type Options struct {
	// ClientName と ClientVersion は hello の client に載せる (rex の client 一覧に出る)。
	ClientName    string
	ClientVersion string
	// EventBuffer が正なら、event をこの長さの queue (Events) へ渡す。0 なら event を捨てる。
	EventBuffer int
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

	// events は event の queue。EventBuffer が 0 なら nil。読む goroutine だけが送り、終わるときに閉じる。
	events chan Event
	done   chan struct{}
	cancel context.CancelFunc
}

// Event は server が送った event 1 件。
type Event struct {
	// Seq はこの接続で送られた event の通し番号 (1 から欠けずに増える)。応答の seq と比べて、結果がどの event まで
	// 含んでいるかを決める。
	Seq int64
	// Payload は event message の payload (`{"type": <event の名前>, "payload": <欄>}`)。
	Payload json.RawMessage
}

type response struct {
	payload json.RawMessage
	seq     int64
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
	if opts.EventBuffer > 0 {
		c.events = make(chan Event, opts.EventBuffer)
	}
	go c.readLoop(readCtx)
	return c, nil
}

// handshake は hello を送り、welcome の type と protocol と revision を確かめる。api_min_revision を api_revision と
// 同じにして、server に古い revision へ落とさせない (応答の形が daemon の読む形と変わる)。落とせない server は
// 1008 で接続を断り、welcome の Read が理由を添えて失敗する。api_min_revision を知らない古い server は
// 別の revision を選びうるので、welcome の revision も確かめる。
// rex-premise: preview.30 api hello の api_min_revision を server が対応できなければ 1008 で閉じる。welcome の api_revision が選ばれた revision
func handshake(ctx context.Context, ws *websocket.Conn, opts Options) (welcome, error) {
	hello, err := json.Marshal(map[string]any{
		"type":             "hello",
		"api_revision":     APIRevision,
		"api_min_revision": APIRevision,
		"client":           map[string]any{"kind": "agent", "name": opts.ClientName, "version": opts.ClientVersion},
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
	if m.APIRevision != APIRevision {
		return welcome{}, fmt.Errorf("rex の server が API revision %d を選んだ (daemon は %d を話す。rex か agents-daemon を更新する)", m.APIRevision, APIRevision)
	}
	return welcome{apiRevision: m.APIRevision, maxMessageBytes: max(m.Limits.MaxMessageBytes, minMessageBytes)}, nil
}

// Call は method を呼び、response の payload を out へ読む (out が nil なら読まない)。server の失敗は code を持つ
// error で返し、対象が無い失敗は IsNotFound で見分ける。
// ctx が終われば待つのをやめる (server は request を走らせ終えることがある)。
func (c *Conn) Call(ctx context.Context, method string, payload, out any) error {
	_, err := c.CallSeq(ctx, method, payload, out)
	return err
}

// CallSeq は Call と同じく呼び、応答の seq も返す。seq は server が request を始めた時点でこの接続に積んでいた
// 最後の event の seq で、結果はそこまでの event を含む。応答はまだ届いていない event を追い越しうる。
// rex-premise: preview.30 api 応答の seq は request を始めた時点で積んでいた最後の event の seq (無ければ 0)
func (c *Conn) CallSeq(ctx context.Context, method string, payload, out any) (int64, error) {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return 0, fmt.Errorf("rex %s: %w: %w", method, ErrNotSent, err)
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
		return 0, fmt.Errorf("rex %s: request を組めない: %w", method, err)
	}
	// Write の ctx が途中で終わると接続ごと閉じる (ライブラリの仕様)。書き込みは数 ms で済み、
	// 書けないなら接続が壊れているので、呼び手の ctx をそのまま渡す。
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		return 0, fmt.Errorf("rex %s: %w: %w", method, ErrNotSent, err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			e := *r.err
			e.method = method
			return r.seq, &e
		}
		if out == nil {
			return r.seq, nil
		}
		if err := json.Unmarshal(r.payload, out); err != nil {
			return r.seq, fmt.Errorf("rex %s: 応答の parse に失敗: %w (payload=%q)", method, err, r.payload)
		}
		return r.seq, nil
	case <-ctx.Done():
		return 0, fmt.Errorf("rex %s: %w", method, ctx.Err())
	case <-c.done:
		return 0, fmt.Errorf("rex %s: 応答の前に接続が切れた: %w", method, c.Err())
	}
}

// Done は接続が終わったら閉じる。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Events は event を届いた順に渡す。接続が終わると、queue に残った event を渡し終えてから閉じる。EventBuffer が 0 の
// 接続では nil。
func (c *Conn) Events() <-chan Event { return c.events }

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

// readLoop は接続が終わるまで message を読み、response を待ち手へ、event を queue へ渡す。queue の空きは待たず、
// 溢れたら close frame (1008) で接続を閉じる (読み手を待つと応答の受け取りが止まる。frame 無しで切ると server は
// client を 60 秒残す)。溢れた後の event は捨て、close の握手が済んで Read が終わるまで読み続ける。
// 読み続けることで server の ping にも答え、event の backlog で server に切られない。
func (c *Conn) readLoop(ctx context.Context) {
	overflowed := false
	defer func() {
		c.cancel()
		_ = c.ws.CloseNow()
		if c.events != nil {
			close(c.events)
		}
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
		switch m.Type {
		case "response":
			c.deliver(m)
		case "event":
			if c.events == nil || overflowed {
				continue
			}
			select {
			case c.events <- Event{Seq: m.Seq, Payload: m.Payload}:
			default:
				overflowed = true
				c.fail(errEventOverflow)
				// Close は相手の close frame を待つので、読むこの goroutine の外で送る。
				go func() { _ = c.ws.Close(websocket.StatusPolicyViolation, "event queue overflow") }()
			}
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
	r := response{payload: m.Payload, seq: m.Seq}
	if m.Error != nil {
		r.err = &apiError{code: m.Error.Code, message: m.Error.Message, status: m.Error.Status}
	}
	ch <- r
}
