package rexcli

import (
	"context"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

type watchState struct{}

func (w *watchState) synced() bool { return false }

func (w *watchState) panes(sessions map[string]string) ([]multiplexer.Pane, bool) { return nil, false }

func (c *execClient) Watch(ctx context.Context, logf func(format string, args ...any)) {
	c.watch = &watchState{}
}
