package rexcli

import (
	"context"
	"errors"
)

// SessionBlocks は session の block を flavor を問わず数える (daemon が空になった起こした session を閉じる。#557)。
// 購読の cache は shell の block しか持たないので、毎回 list_blocks で引く。session が無ければ found=false。
func (c *execClient) SessionBlocks(ctx context.Context, sessionID string) (n int, found bool, err error) {
	blocks, err := c.listBlocks(ctx, sessionID)
	if err != nil {
		if isNotFound(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return len(blocks.Blocks), true, nil
}

// SessionKill は session を ID で閉じる。中の block も確認なしで閉じる。
// rex-premise: preview.30 cli session kill は session ID を受ける
func (c *execClient) SessionKill(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("rex session kill: session ID が空")
	}
	_, err := c.cli.Run(ctx, "session", "kill", sessionID)
	return err
}
