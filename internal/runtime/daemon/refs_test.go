package daemon

import (
	"context"
	"path/filepath"
	"reflect"
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
	r.refPoll = refpoll.New(r.edges, r.refs, func(context.Context, ref.Item) (refpoll.Remote, error) { return refpoll.Remote{State: "merged"}, nil }, r.liveRefSessions, r.log)
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

// PR を出したセッションを引く cwd は、作業の記録の cwd (hook が渡す今の cwd。セッションの途中で入った worktree も
// 映る) を pane の cwd (起動したディレクトリ) より先に使う。記録が無ければ pane の cwd。
func TestLiveRefSessions_作業の記録のcwdを先に使う(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, &multiplexerfake.Client{}, now)
	r.attachStateDir(filepath.Dir(statusPath))
	in := `{"session_id":"A","cwd":"/repo/.claude/worktrees/x","hook_event_name":"UserPromptSubmit","prompt":"直して"}`
	if _, err := r.works.Ingest([]byte(in), now); err != nil {
		t.Fatal(err)
	}
	r.livePlaces = []refpoll.Session{{ID: "A", CWD: "/repo"}, {ID: "B", CWD: "/other"}}

	want := []refpoll.Session{{ID: "A", CWD: "/repo/.claude/worktrees/x"}, {ID: "B", CWD: "/other"}}
	if got := r.liveRefSessions(); !reflect.DeepEqual(got, want) {
		t.Errorf("liveRefSessions() = %v, want %v", got, want)
	}
}
