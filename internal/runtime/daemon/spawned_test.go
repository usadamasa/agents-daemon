package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/reaper"
)

// reaperClient は session の後片付けの操作を持つ fake。blocks は session → block の数 (無ければ not found)。
type reaperClient struct {
	multiplexerfake.Client
	blocks map[string]int
	killed []string
}

func (c *reaperClient) SessionBlocks(_ context.Context, sessionID string) (int, bool, error) {
	n, ok := c.blocks[sessionID]
	return n, ok, nil
}

func (c *reaperClient) SessionKill(_ context.Context, sessionID string) error {
	c.killed = append(c.killed, sessionID)
	return nil
}

// housekeeping は起こした session の印を古さで消さない。印を消すのは reaper だけで、session を閉じたときか session が
// もう無いとき。daemon が回らないまま日が経った (眠っていた、idle で終わっていた) 後でも、生きている session の印は
// 残り、pane が空になれば閉じられる。
func TestHousekeep_keepsSpawnedMarks(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	r, statusPath := newTestRuntime(t, &multiplexerfake.Client{}, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	if err := r.spawned.Put("session:old"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(r.spawned.Dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("印のファイル = %v (%v)", entries, err)
	}
	old := now.Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(r.spawned.Dir, entries[0].Name()), old, old); err != nil {
		t.Fatal(err)
	}
	r.housekeep(filepath.Join(dir, "logs", "daemon.log"), now)
	if _, ok := r.spawned.Get("session:old"); !ok {
		t.Error("housekeeping が 1 年前の印を消した (印は reaper だけが消す)")
	}
}

// tick は PaneList の後に起こした session の印を回る (回り方は reaper のテスト)。
func TestTick_ReapsSpawnedSessions(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	client := &reaperClient{blocks: map[string]int{"session:empty": 0}}
	r, statusPath := newTestRuntime(t, client, now)
	r.attachStateDir(filepath.Dir(statusPath))
	if err := r.spawned.Put("session:empty"); err != nil {
		t.Fatal(err)
	}
	r.reaper = reaper.New(r.spawned, client, r.log, false)
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if !slices.Equal(client.killed, []string{"session:empty"}) {
		t.Errorf("閉じた session = %q, want [session:empty]", client.killed)
	}
}
