package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// watchingClient は購読の口 (multiplexer.Watcher) を持つ fake。
type watchingClient struct {
	*multiplexerfake.Client
	watched int
}

func (c *watchingClient) Watch(context.Context, func(string, ...any)) { c.watched++ }

// daemon は購読の口を持つ client の購読を始める。dry-run で包んでも読み取りの購読は通す。
func TestStartWatch(t *testing.T) {
	log := applog.NewLogger(filepath.Join(t.TempDir(), "daemon.log"), time.Now)
	t.Cleanup(func() { _ = log.Close() })

	t.Run("購読の口を持つ client", func(t *testing.T) {
		c := &watchingClient{Client: &multiplexerfake.Client{}}
		startWatch(context.Background(), c, log)
		if c.watched != 1 {
			t.Errorf("Watch の呼び出し = %d 回, want 1", c.watched)
		}
	})
	t.Run("dry-run で包んだ client", func(t *testing.T) {
		c := &watchingClient{Client: &multiplexerfake.Client{}}
		startWatch(context.Background(), newDryRunClient(c, log), log)
		if c.watched != 1 {
			t.Errorf("Watch の呼び出し = %d 回, want 1 (dry-run の包みが購読を隠した)", c.watched)
		}
	})
	t.Run("購読の口を持たない client は何もしない", func(t *testing.T) {
		c := &multiplexerfake.Client{}
		startWatch(context.Background(), c, log)
		if len(c.Calls) != 0 {
			t.Errorf("Calls = %v, want 空", c.Calls)
		}
	})
}
