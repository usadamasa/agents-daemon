package herdrcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeSocketServer は herdr の API socket の代わり｡接続ごとに改行区切りの request を 1 本読み､
// respond が返す 1 行を書く｡unix socket は sandbox が bind を許さないので net.Pipe で繋ぐ｡
type fakeSocketServer struct {
	Requests []map[string]any
	respond  func(req map[string]any) string
}

func (s *fakeSocketServer) dial(context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		line, err := bufio.NewReader(server).ReadString('\n')
		if err != nil {
			return
		}
		var req map[string]any
		if json.Unmarshal([]byte(line), &req) != nil {
			return
		}
		s.Requests = append(s.Requests, req)
		_, _ = server.Write([]byte(s.respond(req) + "\n"))
	}()
	return client, nil
}

func newSocketClientForTest(respond func(req map[string]any) string) (*SocketClient, *fakeSocketServer) {
	srv := &fakeSocketServer{respond: respond}
	return &SocketClient{path: "fake.sock", dial: srv.dial}, srv
}

func requestMethod(req map[string]any) string {
	m, _ := req["method"].(string)
	return m
}

func requestParams(req map[string]any) map[string]any {
	p, _ := req["params"].(map[string]any)
	return p
}

func TestSocketClient_PaneCurrent_SendsCallerPaneAndParsesPane(t *testing.T) {
	// 実機 herdr 0.9.3 の `pane.current` の応答そのまま (余分なフィールドも含む)｡
	fixture := `{"id":"x","result":{"pane":{"agent_status":"unknown","cwd":"/Users/u","focused":true,"foreground_cwd":"/Users/u/wt","pane_id":"w3X:p1","revision":1,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"w3X:t1","terminal_id":"term_1","terminal_title":"✳ fix the bug","terminal_title_stripped":"fix the bug","workspace_id":"w3X"},"type":"pane_current"}}`
	c, srv := newSocketClientForTest(func(map[string]any) string { return fixture })

	pane, err := c.PaneCurrent(context.Background(), "w3X:p1")
	if err != nil {
		t.Fatalf("PaneCurrent() error = %v", err)
	}
	want := PaneView{PaneID: "w3X:p1", TabID: "w3X:t1", WorkspaceID: "w3X", TerminalTitleStripped: "fix the bug", CWD: "/Users/u", ForegroundCWD: "/Users/u/wt"}
	if pane != want {
		t.Errorf("PaneCurrent() = %+v, want %+v", pane, want)
	}
	if len(srv.Requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(srv.Requests))
	}
	req := srv.Requests[0]
	if requestMethod(req) != "pane.current" {
		t.Errorf("method = %q, want pane.current", requestMethod(req))
	}
	// caller_pane_id を落とすと focused pane (別 workspace の shell のこともある) が返り､別のタブを rename する｡
	if got := requestParams(req)["caller_pane_id"]; got != "w3X:p1" {
		t.Errorf("params.caller_pane_id = %v, want w3X:p1", got)
	}
	if id, _ := req["id"].(string); id == "" {
		t.Errorf("request に id が無い")
	}
}

func TestSocketClient_PaneCurrent_ErrorEnvelope(t *testing.T) {
	// 実機で確認した pane_not_found の応答｡
	c, _ := newSocketClientForTest(func(map[string]any) string {
		return `{"error":{"code":"pane_not_found","message":"pane not found"},"id":"x"}`
	})

	_, err := c.PaneCurrent(context.Background(), "w3X:p9")
	var envErr *envelopeError
	if !errors.As(err, &envErr) || envErr.Code != "pane_not_found" {
		t.Fatalf("PaneCurrent() error = %v, want *envelopeError pane_not_found", err)
	}
}

func TestSocketClient_ReportWorkspaceMetadata_EmptyTokenIsNull(t *testing.T) {
	c, srv := newSocketClientForTest(func(map[string]any) string { return `{"id":"x","result":{}}` })

	err := c.ReportWorkspaceMetadata(context.Background(), "w3X", "claude-config:claude-title", 1234,
		map[string]string{"claude": "fix the bug", "branch": ""})
	if err != nil {
		t.Fatalf("ReportWorkspaceMetadata() error = %v", err)
	}
	req := srv.Requests[0]
	if requestMethod(req) != "workspace.report_metadata" {
		t.Errorf("method = %q", requestMethod(req))
	}
	p := requestParams(req)
	if p["workspace_id"] != "w3X" || p["source"] != "claude-config:claude-title" {
		t.Errorf("params = %v", p)
	}
	if seq, _ := p["seq"].(float64); int64(seq) != 1234 {
		t.Errorf("params.seq = %v, want 1234", p["seq"])
	}
	tokens, _ := p["tokens"].(map[string]any)
	if tokens["claude"] != "fix the bug" {
		t.Errorf("tokens.claude = %v", tokens["claude"])
	}
	// 空の値は null で送り､herdr 側の token を消す (空文字列だと空の行が残る)｡
	if v, ok := tokens["branch"]; !ok || v != nil {
		t.Errorf("tokens.branch = %v (present=%v), want null", v, ok)
	}
}

func TestSocketClient_TabGet_And_TabRename(t *testing.T) {
	c, srv := newSocketClientForTest(func(req map[string]any) string {
		if requestMethod(req) == "tab.get" {
			// 実機 herdr 0.9.3 の `tab.get` の応答そのまま｡
			return `{"id":"x","result":{"tab":{"agent_status":"unknown","focused":true,"label":"1","number":1,"pane_count":1,"tab_id":"w3X:t1","workspace_id":"w3X"},"type":"tab_info"}}`
		}
		return `{"id":"x","result":{}}`
	})

	tab, err := c.TabGet(context.Background(), "w3X:t1")
	if err != nil {
		t.Fatalf("TabGet() error = %v", err)
	}
	if tab != (TabView{TabID: "w3X:t1", Label: "1", Number: 1}) {
		t.Errorf("TabGet() = %+v", tab)
	}
	if err := c.TabRename(context.Background(), "w3X:t1", "fix the bug"); err != nil {
		t.Fatalf("TabRename() error = %v", err)
	}
	req := srv.Requests[1]
	if requestMethod(req) != "tab.rename" {
		t.Errorf("method = %q", requestMethod(req))
	}
	p := requestParams(req)
	if p["tab_id"] != "w3X:t1" || p["label"] != "fix the bug" {
		t.Errorf("params = %v", p)
	}
}

func TestSocketClient_Call_HonorsContextDeadline(t *testing.T) {
	// 応答を返さない server｡ctx の期限で戻らないと hook が Claude Code を待たせる｡
	c := &SocketClient{path: "fake.sock", dial: func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			_, _ = bufio.NewReader(server).ReadString('\n')
			// 読むだけで返さない｡client が閉じるまで持つ｡
			time.Sleep(2 * time.Second)
			_ = server.Close()
		}()
		return client, nil
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.TabGet(ctx, "w3X:t1")
	if err == nil {
		t.Fatalf("TabGet() error = nil, want deadline error")
	}
	if time.Since(start) > time.Second {
		t.Errorf("TabGet() took %v, want it bounded by the ctx deadline", time.Since(start))
	}
}

func TestSocketClient_Call_DialFailureNamesSocket(t *testing.T) {
	c := NewSocketClient("/nonexistent/dir/none.sock")
	_, err := c.TabGet(context.Background(), "w3X:t1")
	if err == nil || !strings.Contains(err.Error(), "none.sock") {
		t.Fatalf("TabGet() error = %v, want it to name the socket path", err)
	}
}

// 実 herdr の socket に対する読み取りだけの確認｡herdr の pane の中 (HERDR_SOCKET_PATH と HERDR_PANE_ID がある)
// でだけ走る｡書き換える method (report_metadata / rename) は呼ばない｡
func TestSocketClient_RealHerdr_PaneCurrentAndTabGet(t *testing.T) {
	socketPath, paneID := os.Getenv("HERDR_SOCKET_PATH"), os.Getenv("HERDR_PANE_ID")
	if socketPath == "" || paneID == "" {
		t.Skip("herdr の pane の中ではないので skip する (HERDR_SOCKET_PATH / HERDR_PANE_ID が無い)")
	}
	c := NewSocketClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pane, err := c.PaneCurrent(ctx, paneID)
	if err != nil {
		t.Fatalf("PaneCurrent(%s) error = %v", paneID, err)
	}
	if pane.TabID == "" || pane.WorkspaceID == "" {
		t.Fatalf("PaneCurrent() = %+v, want tab_id と workspace_id", pane)
	}
	tab, err := c.TabGet(ctx, pane.TabID)
	if err != nil {
		t.Fatalf("TabGet(%s) error = %v", pane.TabID, err)
	}
	if tab.Number <= 0 {
		t.Errorf("TabGet() = %+v, want number > 0", tab)
	}
}
