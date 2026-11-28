package rex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
)

// ErrNoAppClient は、この機械の Rex.app が server に繋がっていないことを表す。
var ErrNoAppClient = errors.New("この機械の Rex.app が rex の server に繋がっていない")

// ErrAppRefused は Rex.app が action を断ったことを表す。Rex.app は Remote Control が無効だと、ほかの client からの
// action を受けない。
var ErrAppRefused = errors.New("操作を Rex.app が断った (Rex.app の Settings で Remote Control を有効にする)")

// performWait は Rex.app の返事を待つ長さ。control API の request の制限 (defaultTimeout) とアプリの exec の制限
// (どちらも 10 秒) より短くし、返事が無いときに呼び手が理由を受け取れるようにする。
const performWait = 3 * time.Second

// selectAction は Rex.app が表示する session と window を切り替える action。server の session.focus_window は
// server の持つ active window を変えるだけで、Rex.app の表示は動かない (#370 の実測)。
// rex-premise: preview.37 api app の client の session.select は session_id と window_id を取り、表示をその window へ切り替える
const selectAction = "session.select"

// ShowWindow はこの機械の Rex.app に、session の window を表示させる (focus.Host)。ほかの機械から繋いだ app の client
// (iOS など) には頼まない。利用者が今見ている画面を動かすための操作なので。
func (c *execClient) ShowWindow(ctx context.Context, session, windowID string) error {
	clients, err := c.rex.ListClients(ctx)
	if err != nil {
		return err
	}
	args := map[string]any{"session_id": session, "window_id": windowID}
	asked := false
	for _, cl := range clients {
		if cl.Kind != "app" || cl.Transport != "unix" {
			continue
		}
		asked = true
		if err := c.rex.Perform(ctx, cl.ClientID, selectAction, args, performWait); err != nil {
			if api.IsRefused(err) {
				return fmt.Errorf("%w: %w", ErrAppRefused, err)
			}
			return err
		}
	}
	if !asked {
		return ErrNoAppClient
	}
	return nil
}
