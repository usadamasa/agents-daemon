package rexcli

import (
	"context"
	"fmt"
)

// keyEvents は SendKeys が受ける key の名前と、block.event の key の payload。daemon が送るのは esc と enter だけで、
// CLI の send-key の key binding の構文 (ctrl+c など) は持たない。text は空でも block は key として受ける
// (2026-11-17 に cat -v へ届く文字が CLI の send-key と同じことを実機で確かめた)。
// rex-premise: preview.30 api block.event の key の payload は key、mods、consumed_mods、text、unshifted_codepoint、composing、action
var keyEvents = map[string]struct {
	key       string
	codepoint int
}{
	"enter": {key: "enter", codepoint: 13},
	"esc":   {key: "escape", codepoint: 27},
}

// SendText は文面を端末に打つ (Enter は押さない)。
func (c *execClient) SendText(ctx context.Context, paneID, text string) error {
	session, err := c.rex.BlockSession(ctx, paneID)
	if err != nil {
		return err
	}
	return c.rex.BlockCall(ctx, session, paneID, "write", map[string]any{"data": text}, nil)
}

// SendKeys は key ごとに press と release を送る (CLI の send-key と同じ)。知らない名前があれば何も送らない。
// block.event は同じ接続の中で送った順に届く。
func (c *execClient) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	for _, k := range keys {
		if _, ok := keyEvents[k]; !ok {
			return fmt.Errorf("rex へ送れない key %q (送れるのは esc と enter)", k)
		}
	}
	session, err := c.rex.BlockSession(ctx, paneID)
	if err != nil {
		return err
	}
	for _, k := range keys {
		ev := keyEvents[k]
		for _, action := range []string{"press", "release"} {
			payload := map[string]any{
				"key": ev.key, "mods": "", "consumed_mods": "", "text": "",
				"unshifted_codepoint": ev.codepoint, "composing": false, "action": action,
			}
			req := map[string]any{"session_id": session, "block_id": paneID, "event": map[string]any{"type": "key", "payload": payload}}
			if err := c.rex.Call(ctx, "block.event", req, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
