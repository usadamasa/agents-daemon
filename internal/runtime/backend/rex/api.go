package rex

import (
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/cli"
)

// Close は control 接続を閉じる。
func (c *execClient) Close() error { return c.rex.Close() }

// isNotFound は対象 (block、session) が無いことによる失敗かを返す。CLI の exit 3 と API の not_found の両方を拾う。
func isNotFound(err error) bool {
	return cli.TargetNotFound(err) || api.IsNotFound(err)
}
