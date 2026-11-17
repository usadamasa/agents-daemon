package control_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control/controltest"
)

func dial(t *testing.T, srv *controltest.Server, opts control.Options) *control.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := control.Dial(ctx, srv.Path, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDialSendsHelloWithRevisionAndClient(t *testing.T) {
	srv := controltest.New(t)
	c := dial(t, srv, control.Options{ClientName: "agents-daemon", ClientVersion: "1.2.3"})

	hellos := srv.Hellos()
	if len(hellos) != 1 {
		t.Fatalf("hello が %d 回 (1 回のはず)", len(hellos))
	}
	var h struct {
		Type           string `json:"type"`
		APIRevision    int    `json:"api_revision"`
		APIMinRevision int    `json:"api_min_revision"`
		Client         struct {
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"client"`
	}
	if err := json.Unmarshal(hellos[0], &h); err != nil {
		t.Fatalf("hello の parse: %v", err)
	}
	// api_min_revision も同じ値にし、古い revision へ黙って落とさせない (落とせない server は 1008 で断る)。
	if h.Type != "hello" || h.APIRevision != control.APIRevision || h.APIMinRevision != control.APIRevision || h.Client.Kind != "agent" ||
		h.Client.Name != "agents-daemon" || h.Client.Version != "1.2.3" {
		t.Errorf("hello = %s", hellos[0])
	}
	if got := c.WelcomeRevision(); got != 14 {
		t.Errorf("welcome の api_revision = %d、14 のはず", got)
	}
}

func TestDialRejectsWelcomeOfOtherProtocol(t *testing.T) {
	srv := controltest.New(t)
	srv.SetWelcome(map[string]any{"type": "welcome", "protocol": "rex.control.v2", "limits": map[string]any{"max_message_bytes": 65536}})
	_, err := control.Dial(callCtx(t), srv.Path, control.Options{})
	if err == nil || !strings.Contains(err.Error(), "rex.control.v2") {
		t.Fatalf("Dial の error = %v、protocol の食い違いを言うはず", err)
	}
}

// welcome が別の revision を選んだら (api_min_revision を知らない server)、その接続の応答の形は daemon の読む形と
// 違うので Dial を失敗にする。
func TestDialRejectsOtherRevision(t *testing.T) {
	srv := controltest.New(t)
	srv.SetWelcome(map[string]any{"type": "welcome", "protocol": "rex.control.v1", "api_revision": 13, "limits": map[string]any{"max_message_bytes": 65536}})
	_, err := control.Dial(callCtx(t), srv.Path, control.Options{})
	if err == nil || !strings.Contains(err.Error(), "13") {
		t.Fatalf("Dial の error = %v、welcome の revision 13 を言うはず", err)
	}
}

func TestDialFailsWhenSocketIsMissing(t *testing.T) {
	_, err := control.Dial(callCtx(t), t.TempDir()+"/none.sock", control.Options{})
	if err == nil {
		t.Fatal("socket が無いのに Dial が成功した")
	}
}

func TestCallDecodesResult(t *testing.T) {
	srv := controltest.New(t)
	srv.Reply("server.status", map[string]any{"pid": 42})
	c := dial(t, srv, control.Options{})

	var st struct {
		PID int `json:"pid"`
	}
	if err := c.Call(callCtx(t), "server.status", nil, &st); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if st.PID != 42 {
		t.Errorf("pid = %d", st.PID)
	}
}

func TestCallSendsPayload(t *testing.T) {
	srv := controltest.New(t)
	srv.Reply("session.list_blocks", map[string]any{"blocks": []any{}})
	c := dial(t, srv, control.Options{})
	if err := c.Call(callCtx(t), "session.attach_all", map[string]any{"event_scope": "all"}, nil); err != nil {
		t.Fatalf("attach_all: %v", err)
	}
	if err := c.Call(callCtx(t), "session.list_blocks", map[string]any{"session_id": "session:a"}, nil); err != nil {
		t.Fatalf("list_blocks: %v", err)
	}
	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if last.Method != "session.list_blocks" || string(last.Payload) != `{"session_id":"session:a"}` {
		t.Errorf("最後の request = %s %s", last.Method, last.Payload)
	}
}

func TestCallReturnsServerError(t *testing.T) {
	srv := controltest.New(t)
	c := dial(t, srv, control.Options{})
	if err := c.Call(callCtx(t), "session.attach_all", map[string]any{"event_scope": "all"}, nil); err != nil {
		t.Fatalf("attach_all: %v", err)
	}
	srv.Fail("com.superlogical.terminal.program_status", controltest.NotFound("block:x"))
	srv.Fail("session.list_blocks", controltest.SessionNotFound("session:x"))
	srv.Fail("server.status", &controltest.Error{Code: "internal", Message: "boom", Status: 500})

	err := c.Call(callCtx(t), "com.superlogical.terminal.program_status", map[string]any{"session_id": "s", "block_id": "block:x"}, nil)
	if code, status := control.ErrorCode(err); code != "not_found" || status != 404 {
		t.Fatalf("error = %v、not_found (404) のはず", err)
	}
	if !control.IsNotFound(err) {
		t.Error("not_found を IsNotFound が拾わない")
	}
	err = c.Call(callCtx(t), "session.list_blocks", map[string]any{"session_id": "session:x"}, nil)
	if !control.IsNotFound(err) {
		t.Errorf("session_not_found を IsNotFound が拾わない: %v", err)
	}
	err = c.Call(callCtx(t), "server.status", nil, nil)
	if err == nil || control.IsNotFound(err) {
		t.Errorf("internal の error = %v、not found 以外の失敗のはず", err)
	}
	if !strings.Contains(err.Error(), "server.status") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error の文面 %q に method と message が無い", err)
	}
}

func TestCallBeforeAttachIsConflict(t *testing.T) {
	srv := controltest.New(t)
	srv.Reply("com.superlogical.terminal.size", map[string]any{"rows": 1})
	c := dial(t, srv, control.Options{})
	err := c.Call(callCtx(t), "com.superlogical.terminal.size", map[string]any{"session_id": "s", "block_id": "b"}, nil)
	if code, _ := control.ErrorCode(err); code != "conflict" {
		t.Fatalf("attach 前の error = %v、conflict のはず", err)
	}
}

// 並んだ Call はそれぞれ自分の応答を受ける (id で突き合わせる)。
func TestConcurrentCallsGetOwnResponses(t *testing.T) {
	srv := controltest.New(t)
	srv.Handle("echo", func(p json.RawMessage) (any, error) { return json.RawMessage(p), nil })
	c := dial(t, srv, control.Options{})
	const n = 50
	errs := make(chan error, n)
	for i := range n {
		go func() {
			var out struct {
				N int `json:"n"`
			}
			if err := c.Call(callCtx(t), "echo", map[string]any{"n": i}, &out); err != nil {
				errs <- err
				return
			}
			if out.N != i {
				errs <- errors.New("他の request の応答を受けた")
				return
			}
			errs <- nil
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// 応答は welcome の max_message_bytes まで受ける (ライブラリの既定の 32 KiB で切らない)。
func TestCallReceivesLargeResult(t *testing.T) {
	srv := controltest.New(t)
	big := strings.Repeat("x", 200*1024)
	srv.Reply("com.superlogical.terminal.format", map[string]any{"content": big})
	c := dial(t, srv, control.Options{})
	if err := c.Call(callCtx(t), "session.attach_all", map[string]any{"event_scope": "all"}, nil); err != nil {
		t.Fatalf("attach_all: %v", err)
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := c.Call(callCtx(t), "com.superlogical.terminal.format", map[string]any{"session_id": "s", "block_id": "b"}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(out.Content) != len(big) {
		t.Errorf("content の長さ = %d", len(out.Content))
	}
}

func TestCallHonorsContext(t *testing.T) {
	srv := controltest.New(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv.Handle("slow", func(json.RawMessage) (any, error) { <-block; return nil, nil })
	c := dial(t, srv, control.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v、DeadlineExceeded のはず", err)
	}
}

// event を読む口はまだ無い。event が流れても応答を受け続ける (読み手を待って止まらない)。
func TestEventsDoNotBlockResponses(t *testing.T) {
	srv := controltest.New(t)
	srv.Reply("server.status", map[string]any{"pid": 1})
	c := dial(t, srv, control.Options{})
	if err := c.Call(callCtx(t), "session.attach_all", map[string]any{"event_scope": "all"}, nil); err != nil {
		t.Fatalf("attach_all: %v", err)
	}
	for range 100 {
		srv.Emit(map[string]any{"type": "session_event"})
	}
	if err := c.Call(callCtx(t), "server.status", nil, nil); err != nil {
		t.Fatalf("event の後の Call: %v", err)
	}
}

func TestServerDisconnectFailsPendingAndLaterCalls(t *testing.T) {
	srv := controltest.New(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv.Handle("slow", func(json.RawMessage) (any, error) { <-block; return nil, nil })
	c := dial(t, srv, control.Options{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(callCtx(t), "slow", nil, nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(srv.Requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	srv.CloseConnections()
	select {
	case err := <-errc:
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("切れた接続の Call の error = %v、切断の error のはず", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("切れても待ちの Call が戻らない")
	}
	<-c.Done()
	if err := c.Call(callCtx(t), "server.status", nil, nil); err == nil {
		t.Error("切れた接続への Call が成功した")
	}
}

func TestCloseEndsConnection(t *testing.T) {
	srv := controltest.New(t)
	c := dial(t, srv, control.Options{})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Close の後に Done が閉じない")
	}
	deadline := time.Now().Add(5 * time.Second)
	for srv.Connections() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.Connections() != 0 {
		t.Error("Close の後も server に接続が残る")
	}
	if err := c.Close(); err != nil {
		t.Errorf("2 回目の Close: %v", err)
	}
}
