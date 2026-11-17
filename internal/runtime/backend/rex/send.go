package rex

import (
	"context"
	"fmt"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
)

// keys は SendKeys が受ける key の名前 (daemon の語彙) と rex の key。daemon が送るのは esc と enter だけで、
// CLI の send-key の key binding の構文 (ctrl+c など) は持たない。
var keys = map[string]api.Key{
	"enter": api.KeyEnter,
	"esc":   api.KeyEscape,
}

// SendText は文面を端末に打つ (Enter は押さない)。
func (c *execClient) SendText(ctx context.Context, paneID, text string) error {
	session, err := c.rex.ResolveBlock(ctx, paneID)
	if err != nil {
		return err
	}
	return c.rex.Write(ctx, session, paneID, text)
}

// SendKeys は key を順に送る。知らない名前があれば何も送らない。
func (c *execClient) SendKeys(ctx context.Context, paneID string, names ...string) error {
	pressed := make([]api.Key, 0, len(names))
	for _, name := range names {
		k, ok := keys[name]
		if !ok {
			return fmt.Errorf("rex へ送れない key %q (送れるのは esc と enter)", name)
		}
		pressed = append(pressed, k)
	}
	session, err := c.rex.ResolveBlock(ctx, paneID)
	if err != nil {
		return err
	}
	return c.rex.PressKeys(ctx, session, paneID, pressed...)
}
