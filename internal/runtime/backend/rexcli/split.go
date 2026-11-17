package rexcli

import (
	"context"
	"errors"
)

// SplitPane は anchor の block の下に block を作り、argv を起こす。
func (c *execClient) SplitPane(ctx context.Context, anchor, label string, argv []string) (string, error) {
	return "", errors.New("未実装")
}

// ClosePane は block を閉じる。
func (c *execClient) ClosePane(ctx context.Context, blockID string) error {
	return errors.New("未実装")
}
