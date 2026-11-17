package rex

import (
	"context"
	"errors"
)

// SessionBlocks は session の block を flavor を問わず数える (daemon が空になった起こした session を閉じる。#557)。
// 購読の cache は shell の block しか持たないので、毎回 list_blocks で引く。session が無ければ found=false。
func (c *execClient) SessionBlocks(ctx context.Context, sessionID string) (n int, found bool, err error) {
	blocks, err := c.rex.ListBlocks(ctx, sessionID)
	if err != nil {
		if isNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return len(blocks.Blocks), true, nil
}

// SessionKill は session を ID で閉じる。中の block も確認なしで閉じる。無い session は not found の error になる。
func (c *execClient) SessionKill(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("rex の session を閉じられない: session ID が空")
	}
	return c.rex.DestroySession(ctx, sessionID)
}
