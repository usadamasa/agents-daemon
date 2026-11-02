package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// tick は見えたセッションを台帳に書き (名前は Claude Code のセッションの登録簿、ListAgents の名前)、同じ tick で
// session:<名前> の端点を台帳のセッションに結びつける。statusline の題 (context/) は名前に使わない。名前の
// セッションがまだ見えなければ結びつけない。
func TestDaemonRuntimeTick_recordsSessionsAndBinds(t *testing.T) {
	panes := []multiplexer.Pane{claudePaneWithSession("p1", "S1", multiplexer.AgentStatusWorking)}
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return panes, nil },
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, client, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	config := t.TempDir()
	r.attachClaudeConfigDir(config)
	writeContextSidecar(t, r.store.Context.Dir, "S1", map[string]any{"session_name": "w の題"})
	writeClaudeSession(t, config, 4242, map[string]any{"sessionId": "S1", "name": "peer", "nameSource": "derived"})

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

// writeClaudeSession は Claude Code のセッションの登録簿 (<config>/sessions/<pid>.json) を 1 つ書く。
func writeClaudeSession(t *testing.T, config string, pid int, fields map[string]any) {
	t.Helper()
	dir := filepath.Join(config, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fields["pid"] = pid
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
