package rexcli

import (
	"context"
	"errors"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
)

// SessionBlocks は session の block を flavor を問わず数える (daemon が空になった起こした session を閉じる。#557)。
// 購読の cache は shell の block しか持たないので、毎回 block ls で引く。session が無ければ found=false。
// rex-premise: preview.28 cli block ls -s は session の全 window の block を返し、無い session は exit 3
func (c *execClient) SessionBlocks(ctx context.Context, sessionID string) (n int, found bool, err error) {
	var blocks blockstate.BlockList
	if err := c.runJSON(ctx, &blocks, "block", "ls", "-s", sessionID, "--json"); err != nil {
		if isTargetNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return len(blocks.Blocks), true, nil
}

// SessionKill は session を ID で閉じる。中の block も確認なしで閉じる。
// rex-premise: preview.28 cli session kill は session ID を受ける
func (c *execClient) SessionKill(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("rex session kill: session ID が空")
	}
	_, err := c.run(ctx, "session", "kill", sessionID)
	return err
}
