package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// tick は見えたセッションを台帳に書き (名前は context/ から)、同じ tick で session:<名前> の端点を台帳の
// セッションに結びつける。名前のセッションがまだ見えなければ結びつけない。
func TestDaemonRuntimeTick_recordsSessionsAndBinds(t *testing.T) {
	panes := []multiplexer.Pane{claudePaneWithSession("p1", "S1", multiplexer.AgentStatusWorking)}
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return panes, nil },
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, client, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	writeContextSidecar(t, r.store.Context.Dir, "S1", map[string]any{"session_name": "peer"})

	e := edge.New(dir)
	if _, err := e.Add(edge.Endpoint{Ref: "issue:o/r#1", CWD: "/w"}, edge.Endpoint{Ref: "session:peer", CWD: "/w"}, "", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Add(edge.Endpoint{Ref: "issue:o/r#1", CWD: "/w"}, edge.Endpoint{Ref: "session:later", CWD: "/w"}, "", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(5*time.Second)); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	list, err := ledger.New(dir).List()
	if err != nil || len(list) != 1 {
		t.Fatalf("台帳 = %+v, %v", list, err)
	}
	if s := list[0]; s.SessionID != "S1" || s.Name != "peer" || s.CWD != "/tmp/project" || !s.FirstSeen.Equal(now) || !s.LastSeen.Equal(now.Add(5*time.Second)) {
		t.Errorf("台帳のセッション = %+v", s)
	}
	refs := map[string]string{}
	edges, _ := e.Find("", "")
	for _, x := range edges {
		written := x.After.Ref
		if x.After.BoundFrom != "" {
			written = x.After.BoundFrom
		}
		refs[written] = x.After.Ref
	}
	if refs["session:peer"] != "sid:S1" || refs["session:later"] != "session:later" {
		t.Errorf("端点 = %v", refs)
	}
}
