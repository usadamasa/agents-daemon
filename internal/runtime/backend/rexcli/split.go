package rexcli

import (
	"context"
	"fmt"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/api"
)

// splitAnchorRatio は SplitPane が anchor (上) に残す取り分。CLI の split --ratio 40 (新しい block が 40%) と同じ
// (2026-11-17 に実機で session.view の split の ratio が 0.6 になることを確かめた)。
const splitAnchorRatio = 0.6

// splitResult は session.new_split の応答。
type splitResult struct {
	BlockIDs []string `json:"block_ids"`
}

// SplitPane は anchor の block の下に block を作り、shell を挟まずに argv を起こして pane の ID を返す (simulate の使い捨て
// pane)。終わっても block を残し、落ちた画面と exit code を読めるようにする。login を切らないと /usr/bin/login を挟んで
// 起き、program の exit code が 0 に化ける (CLI の --shell=none が足していた分。2026-11-17 に実測)。
// rex-premise: preview.30 api new_split の options の shell none は login を既定 (true) のままにすると login を挟む
func (c *execClient) SplitPane(ctx context.Context, anchor, label string, argv []string) (string, error) {
	session, err := c.rex.BlockSession(ctx, anchor)
	if err != nil {
		return "", err
	}
	options := map[string]any{"shell": "none", "login": false, "command": argv, "exit": map[string]any{"on_completion": false}}
	req := map[string]any{
		"session_id": session, "anchor_id": anchor, "direction": "vertical", "side": "after",
		"ratio": splitAnchorRatio, "focus": false, "layout": api.ShellLayout(label, options),
	}
	var res splitResult
	if err := c.rex.Call(ctx, "session.new_split", req, &res); err != nil {
		return "", err
	}
	if len(res.BlockIDs) == 0 || res.BlockIDs[0] == "" {
		return "", fmt.Errorf("rex の session.new_split の応答の block_ids が空で、作った pane が分からない (anchor=%s)", anchor)
	}
	_ = c.rex.Release(ctx, session, res.BlockIDs[:1]) // 手放せなくても pane は動いている (api.Client.Release)
	return res.BlockIDs[0], nil
}

// ClosePane は block を閉じる。無い block は not found の error になる。
func (c *execClient) ClosePane(ctx context.Context, blockID string) error {
	session, err := c.rex.BlockSession(ctx, blockID)
	if err != nil {
		return err
	}
	return c.rex.Call(ctx, "block.close", map[string]any{"session_id": session, "block_id": blockID}, nil)
}
