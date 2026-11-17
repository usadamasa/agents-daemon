// Package controltest は rex server の control API のプロトコルを真似る fake server を提供する。control のテストと、
// rex の状態から応答を組む apitest が使う。
// 実機と同じく hello を先に求め、attach していない接続から session を名指す request を conflict で断る。
package controltest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Subprotocol は control 接続の WebSocket の subprotocol。
const Subprotocol = "rex.control.v1"

// Error は handler が返す失敗。response の error になる。
type Error struct {
	Code    string
	Message string
	Status  int
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// NotFound は実機が無い block に返す失敗。
func NotFound(what string) *Error {
	return &Error{Code: "not_found", Message: "block not found: " + what, Status: 404}
}

// SessionNotFound は実機が無い session に返す失敗。
func SessionNotFound(session string) *Error {
	return &Error{Code: "session_not_found", Message: "session " + session + " not found", Status: 404}
}

// Handler は 1 method の応答を返す。payload は request の payload そのまま。
type Handler func(payload json.RawMessage) (any, error)

// Request は server が受けた request の記録。
type Request struct {
	Method  string
	Payload json.RawMessage
}

// Server は Unix socket で待つ fake の rex server。
type Server struct {
	// Path は待ち受けている socket のパス。
	Path string

	t        testing.TB
	httpSrv  *http.Server
	mu       sync.Mutex
	handlers map[string]Handler
	requests []Request
	conns    map[*serverConn]struct{}
	// Welcome は hello に返す welcome。nil なら既定の形を返す。
	welcome map[string]any
	// hellos は受けた hello。
	hellos []json.RawMessage
	// closeStatuses は client が閉じた接続の close frame の status。
	closeStatuses []websocket.StatusCode
}

// New は socket を開いて fake server を起こす。t の終わりに閉じる。
func New(t testing.TB) *Server {
	t.Helper()
	// macOS の Unix socket のパスは 104 バイトまでで、t.TempDir は長すぎることがある。
	dir, err := os.MkdirTemp("", "rexfake")
	if err != nil {
		t.Fatalf("socket の置き場の作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("socket の listen に失敗: %v", err)
	}
	s := &Server{Path: path, t: t, handlers: map[string]Handler{}, conns: map[*serverConn]struct{}{}}
	s.httpSrv = &http.Server{Handler: http.HandlerFunc(s.serve)} // #nosec G112 -- テストの fake server
	go func() { _ = s.httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		s.CloseConnections()
		_ = s.httpSrv.Close()
	})
	return s
}

// Handle は method の応答を決める。同じ method を 2 回決めたら後のものが勝つ。
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// Reply は method に決まった result を返させる。
func (s *Server) Reply(method string, result any) {
	s.Handle(method, func(json.RawMessage) (any, error) { return result, nil })
}

// Fail は method に決まった失敗を返させる。
func (s *Server) Fail(method string, err *Error) {
	s.Handle(method, func(json.RawMessage) (any, error) { return nil, err })
}

// SetWelcome は hello に返す welcome を差し替える (protocol の食い違いを試す)。
func (s *Server) SetWelcome(w map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.welcome = w
}

// Requests は受けた request を受けた順に返す (session.attach_all と session.attach も含む)。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Hellos は受けた hello を返す。
func (s *Server) Hellos() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]json.RawMessage(nil), s.hellos...)
}

// Emit は session.attach_all を打った全接続へ event を送る。event は event message の payload
// (`{"type":"session_event","payload":{...}}`)。接続ごとに seq を 1 から振る。
func (s *Server) Emit(event any) {
	s.t.Helper()
	b, err := json.Marshal(event)
	if err != nil {
		s.t.Fatalf("event の marshal に失敗: %v", err)
	}
	s.mu.Lock()
	conns := make([]*serverConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.emit(b)
	}
}

// EmitDelayed は Emit と同じく seq をすぐ振るが、届けるのは delay の後にする。seq を振った後に打たれた request の応答が
// event より先に届く (応答の seq がまだ届いていない event を含む) 形を真似る。
func (s *Server) EmitDelayed(event any, delay time.Duration) {
	s.t.Helper()
	b, err := json.Marshal(event)
	if err != nil {
		s.t.Fatalf("event の marshal に失敗: %v", err)
	}
	s.mu.Lock()
	conns := make([]*serverConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		if !c.attachedAll {
			c.mu.Unlock()
			continue
		}
		c.seq++
		msg, _ := json.Marshal(map[string]any{"type": "event", "seq": c.seq, "payload": json.RawMessage(b)})
		c.mu.Unlock()
		go func() {
			time.Sleep(delay)
			_ = c.ws.Write(context.Background(), websocket.MessageText, msg)
		}()
	}
}

// CloseStatuses は client が閉じた接続の close frame の status を、閉じた順に返す。close frame 無しで切れた接続は -1。
func (s *Server) CloseStatuses() []websocket.StatusCode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]websocket.StatusCode(nil), s.closeStatuses...)
}

// Connections は開いている接続の数を返す。
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// CloseConnections は開いている接続を close frame 無しで切る (server の再起動を真似る)。
func (s *Server) CloseConnections() {
	s.mu.Lock()
	conns := make([]*serverConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.ws.CloseNow()
	}
}

type serverConn struct {
	ws *websocket.Conn
	mu sync.Mutex
	// attachedAll は session.attach_all を受けたか。attached は session.attach で attach した session。
	attachedAll bool
	attached    map[string]bool
	seq         int64
}

func (c *serverConn) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = c.ws.Write(context.Background(), websocket.MessageText, b)
}

func (c *serverConn) emit(event []byte) {
	c.mu.Lock()
	if !c.attachedAll {
		c.mu.Unlock()
		return
	}
	c.seq++
	msg := map[string]any{"type": "event", "seq": c.seq, "payload": json.RawMessage(event)}
	b, _ := json.Marshal(msg)
	// seq の順を崩さないよう、書き終わるまで持つ。
	_ = c.ws.Write(context.Background(), websocket.MessageText, b)
	c.mu.Unlock()
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/control" {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		return
	}
	if ws.Subprotocol() != Subprotocol {
		_ = ws.Close(websocket.StatusProtocolError, "protocol error")
		return
	}
	ws.SetReadLimit(1 << 20)
	ctx := context.Background()
	_, hello, err := ws.Read(ctx)
	if err != nil {
		return
	}
	var h struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(hello, &h) != nil || h.Type != "hello" {
		_ = ws.Close(websocket.StatusProtocolError, "protocol error")
		return
	}
	c := &serverConn{ws: ws, attached: map[string]bool{}}
	s.mu.Lock()
	s.hellos = append(s.hellos, hello)
	welcome := s.welcome
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = ws.CloseNow()
	}()
	if welcome == nil {
		welcome = map[string]any{
			"type": "welcome", "protocol": Subprotocol, "api_revision": 14,
			"limits": map[string]any{"max_message_bytes": 1 << 20},
			"client": map[string]any{"id": "client:fake", "reconnect_token": "token"},
		}
	}
	c.write(welcome)
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			s.mu.Lock()
			s.closeStatuses = append(s.closeStatuses, websocket.CloseStatus(err))
			s.mu.Unlock()
			return
		}
		go s.answer(c, b)
	}
}

// answer は 1 request に答える。実機と同じく request は並んで走り、応答の順は request の順と限らない。
func (s *Server) answer(c *serverConn, b []byte) {
	var req struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(b, &req); err != nil || req.Type != "request" {
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: req.Method, Payload: req.Payload})
	h := s.handlers[req.Method]
	s.mu.Unlock()

	c.mu.Lock()
	seq := c.seq
	c.mu.Unlock()
	resp := map[string]any{"type": "response", "id": req.ID, "seq": seq}
	result, err := s.dispatch(c, req.Method, req.Payload, h)
	var fe *Error
	switch {
	case errors.As(err, &fe):
		resp["error"] = map[string]any{"code": fe.Code, "message": fe.Message, "status": fe.Status}
	case err != nil:
		resp["error"] = map[string]any{"code": "internal", "message": err.Error(), "status": 500}
	default:
		if result == nil {
			result = map[string]any{}
		}
		resp["payload"] = result
	}
	c.write(resp)
}

func (s *Server) dispatch(c *serverConn, method string, payload json.RawMessage, h Handler) (any, error) {
	var named struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(payload, &named)
	switch method {
	case "session.attach_all":
		// handler があれば先に通す (購読を断る server を真似る)。断ったら attach しない。
		if h != nil {
			if _, err := h(payload); err != nil {
				return nil, err
			}
		}
		c.mu.Lock()
		c.attachedAll = true
		c.mu.Unlock()
		return map[string]any{}, nil
	case "session.attach":
		c.mu.Lock()
		c.attached[named.SessionID] = true
		c.mu.Unlock()
		if h == nil {
			return map[string]any{}, nil
		}
		return h(payload)
	}
	if named.SessionID != "" {
		c.mu.Lock()
		ok := c.attachedAll || c.attached[named.SessionID]
		c.mu.Unlock()
		if !ok {
			return nil, &Error{Code: "conflict", Message: "stream is not attached to session " + named.SessionID, Status: 409}
		}
	}
	if h == nil {
		return nil, &Error{Code: "method_not_found", Message: "method not found: " + method, Status: 404}
	}
	return h(payload)
}
