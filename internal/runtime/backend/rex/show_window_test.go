package rex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control/controltest"
)

const (
	appClient   = "client:01a21f40-dfe6-7a50-93fe-ebdd74987fab"
	agentClient = "client:01a220ae-6858-7196-8da9-1497437c5e80"
)

// 実機 (Mac preview.37) の client.list から、Rex.app、同じ pid で info の無い接続、daemon、CLI を写した。
const clientList = `{"clients":[` +
	`{"client_id":"` + appClient + `","info":{"kind":"app","platform":"macos","name":"Rex","version":"0.1.0-preview.37","label":"taylor"},"principal":{"kind":"peer","transport":"unix","pid":46413},"connection_state":"connected"},` +
	`{"client_id":"client:01a21f40-e698-7700-98ea-cfeafbd25639","principal":{"kind":"peer","transport":"unix","pid":46413},"connection_state":"connected"},` +
	`{"client_id":"` + agentClient + `","info":{"kind":"agent","name":"agents-daemon","version":"(devel)"},"principal":{"kind":"peer","transport":"unix","pid":2589},"connection_state":"connected"},` +
	`{"client_id":"client:01a220c8-198a-7914-ac04-102ea57783ca","info":{"kind":"cli","platform":"macos","name":"rex","version":"0.1.0-preview.25"},"principal":{"kind":"peer","transport":"unix","pid":58125},"connection_state":"connected"}]}`

func reply(t *testing.T, f *fakeAPI, method, result string) {
	t.Helper()
	f.Protocol().Reply(method, json.RawMessage(result))
}

// TestExecClient_ShowWindow は client.list から Rex.app の client を選び、client.perform で session.select を頼む。
// rex-premise: preview.37 api client.perform は client_id、action、args、timeout_ms を取り、app の session.select は session_id と window_id を取る
func TestExecClient_ShowWindow(t *testing.T) {
	f := newFakeAPI(t)
	reply(t, f, "client.list", clientList)
	reply(t, f, "client.perform", `{"performed_by":"`+appClient+`"}`)
	if err := f.client(staticResolver(nil)).ShowWindow(context.Background(), tabSession, tabWindowB); err != nil {
		t.Fatalf("ShowWindow() error = %v", err)
	}
	reqs := f.RequestsOf("client.perform")
	if len(reqs) != 1 {
		t.Fatalf("client.perform の request = %d 件, want 1 (app の client だけに頼む)", len(reqs))
	}
	var req struct {
		ClientID  string `json:"client_id"`
		Action    string `json:"action"`
		TimeoutMs int    `json:"timeout_ms"`
		Args      struct {
			SessionID string `json:"session_id"`
			WindowID  string `json:"window_id"`
		} `json:"args"`
	}
	if err := json.Unmarshal(reqs[0], &req); err != nil {
		t.Fatalf("client.perform の payload を読めない: %v", err)
	}
	if req.ClientID != appClient || req.Action != "session.select" || req.Args.SessionID != tabSession || req.Args.WindowID != tabWindowB {
		t.Errorf("payload = %+v", req)
	}
	// 呼び手 (アプリは 10 秒で切る) が理由を受け取れるよう、Rex.app の返事を待つ時間は request の制限より短くする。
	if req.TimeoutMs <= 0 || req.TimeoutMs >= int(apiTimeout.Milliseconds()) {
		t.Errorf("timeout_ms = %d, want 0 より大きく request の制限 (%d) より短い", req.TimeoutMs, apiTimeout.Milliseconds())
	}
}

// 繋がっている app の client が無ければ、頼まずに ErrNoAppClient を返す。
func TestExecClient_ShowWindow_NoAppClient(t *testing.T) {
	f := newFakeAPI(t)
	reply(t, f, "client.list", `{"clients":[{"client_id":"`+agentClient+`","info":{"kind":"agent","name":"agents-daemon"},"principal":{"transport":"unix"},"connection_state":"connected"}]}`)
	err := f.client(staticResolver(nil)).ShowWindow(context.Background(), tabSession, tabWindowB)
	if !errors.Is(err, ErrNoAppClient) {
		t.Errorf("ShowWindow() error = %v, want ErrNoAppClient", err)
	}
	if n := len(f.RequestsOf("client.perform")); n != 0 {
		t.Errorf("client.perform の request = %d 件, want 0", n)
	}
}

// ほかの機械から繋いだ app の client (transport が unix でない) には頼まない。
func TestExecClient_ShowWindow_RemoteAppClient(t *testing.T) {
	f := newFakeAPI(t)
	reply(t, f, "client.list", `{"clients":[{"client_id":"client:remote","info":{"kind":"app","platform":"ios","name":"Rex"},"principal":{"transport":"tailscale"},"connection_state":"connected"}]}`)
	err := f.client(staticResolver(nil)).ShowWindow(context.Background(), tabSession, tabWindowB)
	if !errors.Is(err, ErrNoAppClient) {
		t.Errorf("ShowWindow() error = %v, want ErrNoAppClient", err)
	}
}

// Rex.app が断ったら (refused)、ErrAppRefused として返す。
func TestExecClient_ShowWindow_Refused(t *testing.T) {
	f := newFakeAPI(t)
	reply(t, f, "client.list", clientList)
	f.Protocol().Fail("client.perform", &controltest.Error{Code: "refused", Message: "Rex.app is not accepting actions", Status: 403})
	err := f.client(staticResolver(nil)).ShowWindow(context.Background(), tabSession, tabWindowB)
	if !errors.Is(err, ErrAppRefused) {
		t.Errorf("ShowWindow() error = %v, want ErrAppRefused", err)
	}
}
