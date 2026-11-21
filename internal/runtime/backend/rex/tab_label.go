package rex

import (
	"context"
	"errors"
	"fmt"
)

// ErrWindowNotFound は session の view に block の居る window が無いことを表す。
var ErrWindowNotFound = errors.New("block の居る window が無い")

// WindowOf は block の居る window の ID と名前を session.view から引く (tablabel.Host)。
func (c *execClient) WindowOf(ctx context.Context, session, blockID string) (windowID, label string, err error) {
	view, err := c.rex.SessionView(ctx, session)
	if err != nil {
		return "", "", err
	}
	for _, w := range view.Windows {
		for _, layer := range w.Layers {
			for _, b := range layer.Blocks {
				if b.BlockID == blockID {
					return w.WindowID, w.Label, nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("rex の session %s の block %s: %w", session, blockID, ErrWindowNotFound)
}

// SetWindowLabel は window の名前を付ける (tablabel.Host)。
func (c *execClient) SetWindowLabel(ctx context.Context, session, windowID, label string) error {
	return c.rex.SetWindowLabel(ctx, session, windowID, label)
}
