package rex

import (
	"context"
	"errors"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

const (
	tabSession    = "session:01a1f94f-3296-7c40-abc4-548d5065ce64"
	tabWindowA    = "window:01a1fcc2-94b5-7999-8684-d4a8036f375d"
	tabWindowB    = "window:01a1fcc4-b023-7c19-8a7e-cfd78e8cd458"
	tabBlockA     = "block:01a1fcc2-94a7-7543-9219-2ab3f6692b5e"
	tabBlockB     = "block:01a1fcc4-b017-731c-8839-2b2da543dbd3"
	tabBlockSplit = "block:01a1fcc4-b017-731c-8839-2b2da543dbd4"
)

// tabAPI は window を 2 つ持つ session を置いた fake の API を返す。window B は block を 2 つ持つ。
func tabAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.AddUnlisted(tabSession, [2]string{tabBlockA, wire.ShellFlavor}, [2]string{tabBlockB, wire.ShellFlavor}, [2]string{tabBlockSplit, wire.ShellFlavor})
	f.SetView(tabSession, `{"revision":3,"session_id":"`+tabSession+`","label":"work","windows":[`+
		`{"window_id":"`+tabWindowA+`","label":"peer-a","layers":[{"kind":"tiled","blocks":[{"block_id":"`+tabBlockA+`"}]}]},`+
		`{"window_id":"`+tabWindowB+`","label":"peer-b","layers":[{"kind":"tiled","blocks":[{"block_id":"`+tabBlockB+`"},{"block_id":"`+tabBlockSplit+`"}]}]}]}`)
	return f
}

// TestExecClient_WindowOf は block の居る window の ID と label を session.view から引く。split した block も
// 同じ window に当たる。
func TestExecClient_WindowOf(t *testing.T) {
	f := tabAPI(t)
	c := f.client(staticResolver(nil))
	for _, tt := range []struct{ block, window, label string }{
		{tabBlockA, tabWindowA, "peer-a"},
		{tabBlockB, tabWindowB, "peer-b"},
		{tabBlockSplit, tabWindowB, "peer-b"},
	} {
		window, label, err := c.WindowOf(context.Background(), tabSession, tt.block)
		if err != nil {
			t.Fatalf("WindowOf(%s) error = %v", tt.block, err)
		}
		if window != tt.window || label != tt.label {
			t.Errorf("WindowOf(%s) = (%s, %s), want (%s, %s)", tt.block, window, label, tt.window, tt.label)
		}
	}
}

// view に無い block は、閉じた block と同じく not found として返す (呼び手が付け先を取り違えない)。
func TestExecClient_WindowOf_BlockMissing(t *testing.T) {
	f := tabAPI(t)
	_, _, err := f.client(staticResolver(nil)).WindowOf(context.Background(), tabSession, "block:gone")
	if !errors.Is(err, ErrWindowNotFound) {
		t.Errorf("WindowOf(無い block) error = %v, want ErrWindowNotFound", err)
	}
}

// TestExecClient_SetWindowLabel は session.set_window_label に session、window、label をそのまま渡す。
// rex-premise: preview.30 api session.set_window_label は session_id、window_id、label を取る
func TestExecClient_SetWindowLabel(t *testing.T) {
	f := tabAPI(t)
	if err := f.client(staticResolver(nil)).SetWindowLabel(context.Background(), tabSession, tabWindowB, "tab-label-sync"); err != nil {
		t.Fatalf("SetWindowLabel() error = %v", err)
	}
	f.assertCalls(t, "session.set_window_label "+tabSession)
	var req struct {
		SessionID string `json:"session_id"`
		WindowID  string `json:"window_id"`
		Label     string `json:"label"`
	}
	f.payloadOf(t, "session.set_window_label", &req)
	if req.SessionID != tabSession || req.WindowID != tabWindowB || req.Label != "tab-label-sync" {
		t.Errorf("payload = %+v", req)
	}
}

// 無い session への付け直しは失敗にする (黙って成功にしない)。
func TestExecClient_SetWindowLabel_SessionMissing(t *testing.T) {
	f := tabAPI(t)
	err := f.client(staticResolver(nil)).SetWindowLabel(context.Background(), "session:gone", tabWindowB, "x")
	if err == nil {
		t.Fatal("SetWindowLabel(無い session) error = nil")
	}
}
