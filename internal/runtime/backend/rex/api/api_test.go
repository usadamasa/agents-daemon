package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control/controltest"
)

// server が切れた直後の呼び出しは、client が切断を検知する前でも張り直した接続で通る (rex の server の再起動、更新)。
// 切断の検知を待たずに呼ぶので、使い回した接続へ request を送れない形を毎回踏みうる。
func TestClient_CallRightAfterDisconnectRedials(t *testing.T) {
	srv := controltest.New(t)
	srv.Handle("server.status", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	c := api.New(func() (string, error) { return srv.Path, nil }, 5*time.Second)
	t.Cleanup(func() { _ = c.Close() })
	for i := range 50 {
		if _, err := c.Raw(context.Background(), "server.status", nil); err != nil {
			t.Fatalf("%d 回目の切断の直後の Raw() error = %v", i, err)
		}
		srv.CloseConnections()
	}
}
