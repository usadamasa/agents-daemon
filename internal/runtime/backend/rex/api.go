package rex

import "github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"

// Close は control 接続を閉じる。
func (c *execClient) Close() error { return c.rex.Close() }

// isNotFound は対象 (block、session) が無いことによる失敗かを返す。閉じた block と session を読み飛ばす判定に使う。
func isNotFound(err error) bool { return api.IsNotFound(err) }
