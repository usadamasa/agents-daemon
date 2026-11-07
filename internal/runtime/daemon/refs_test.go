package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/refpoll"
)

// tick は辺の外部の参照を引き直す (引き直しの規則は refpoll のテスト)。
func TestDaemonRuntimeTick_refreshesRefs(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
	}
	r, statusPath := newTestRuntime(t, client, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	r.refPoll = refpoll.New(r.edges, r.refs, func(context.Context, ref.Item) (refpoll.Remote, error) { return refpoll.Remote{State: "merged"}, nil }, r.log)
	if _, err := edge.New(dir).Add(edge.Endpoint{Ref: "pr:o/r#1", CWD: "/w"}, edge.Endpoint{Ref: "sid:a", CWD: "/w"}, "", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatal(err)
	}
	got, err := r.refs.States()
	if err != nil {
		t.Fatal(err)
	}
	if got["pr:o/r#1"] != "merged" {
		t.Errorf("写し = %v", got)
	}
}
