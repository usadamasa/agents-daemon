package rex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wrap"
)

// selfBinary は包みが印を書くのに呼ぶ agents-daemon のパス。spawn を走らせている自分 (cache のバイナリ) を指す。
// テストは差し替える (go test のバイナリを包みから起こさない)。
var selfBinary = os.Executable

// rexBinary は包みが叩く rex のパス。テストは fake の rex に差し替える。
var rexBinary = wrap.Locate

// NewSession は新しい rex session を作り、その最初の pane で argv を起こして pane の置き場を返す (spawn.Host)。label は
// session と window の名前。argv は wrap の包みで起こし、終わったら session を閉じる。異常終了では包みが Enter まで画面を
// 残すので、block は終わったら閉じる (rex の既定)。login shell の環境で起こし、CLI の session new と同じ環境と引数になる
// (2026-11-17 に PATH と空白・`$` を含む引数が CLI と同じことを実機で確かめた)。起きたコマンドの場所
// (rexplace.Place) は新しい session の block になる。label の先頭の `-` は外す (CLI が flag と読んで拒んだ名残で、名前を揃える)。
// 外して空なら label を付けない (rex の既定の名前になる)。
func (c *execClient) NewSession(ctx context.Context, label, cwd string, argv []string) (multiplexer.Placement, error) {
	bin, err := rexBinary()
	if err != nil {
		return multiplexer.Placement{}, fmt.Errorf("rex の session を作れない: 包みが叩く rex が分からない: %w", err)
	}
	self, err := selfBinary()
	if err != nil {
		return multiplexer.Placement{}, fmt.Errorf("rex の session を作れない: 印を書く agents-daemon のパスが分からない: %w", err)
	}
	session, window, block, err := c.rex.CreateSession(ctx, strings.TrimLeft(label, "-"), api.Block{CWD: cwd, Command: wrap.SessionCommand(bin, self, argv)})
	if err != nil {
		return multiplexer.Placement{}, err
	}
	return multiplexer.Placement{SessionID: session, WindowID: window, PaneID: block}, nil
}

// NewWindow は既存の rex session (ID か label) にタブ (window) を足し、その pane で argv を起こして pane の置き場を返す
// (spawn.Host)。返す session は resolve した ID。label は window の名前で、先頭の `-` は NewSession と同じく外す。
// 利用者が見ている window は動かさない。argv は wrap のタブの包みで起こし、共有の session には触れない。
// 足し先が無ければ not found の error を返す。
func (c *execClient) NewWindow(ctx context.Context, session, label, cwd string, argv []string) (multiplexer.Placement, error) {
	if session == "" {
		return multiplexer.Placement{}, errors.New("rex のタブを足せない: 足し先の session が空")
	}
	sessionID, err := c.rex.ResolveSession(ctx, session)
	if err != nil {
		return multiplexer.Placement{}, err
	}
	window, block, err := c.rex.NewWindow(ctx, sessionID, strings.TrimLeft(label, "-"), api.Block{CWD: cwd, Command: wrap.TabCommand(argv)})
	if err != nil {
		return multiplexer.Placement{}, err
	}
	return multiplexer.Placement{SessionID: sessionID, WindowID: window, PaneID: block}, nil
}

// splitAnchorRatio は SplitPane が anchor (上) に残す取り分。CLI の split --ratio 40 (新しい block が 40%) と同じ
// (2026-11-17 に実機で session.view の split の ratio が 0.6 になることを確かめた)。
const splitAnchorRatio = 0.6

// SplitPane は anchor の pane の下に pane を作り、shell を挟まずに argv を起こして pane の ID を返す (simulate の使い捨て
// pane)。終わっても pane を残し、落ちた画面と exit code を読めるようにする。
func (c *execClient) SplitPane(ctx context.Context, anchor, label string, argv []string) (string, error) {
	session, err := c.rex.ResolveBlock(ctx, anchor)
	if err != nil {
		return "", err
	}
	return c.rex.NewSplit(ctx, session, anchor, splitAnchorRatio, api.Block{Label: label, Command: argv, NoShell: true, KeepOpen: true})
}

// ClosePane は pane を閉じる。無い pane は not found の error になる。
func (c *execClient) ClosePane(ctx context.Context, paneID string) error {
	session, err := c.rex.ResolveBlock(ctx, paneID)
	if err != nil {
		return err
	}
	return c.rex.CloseBlock(ctx, session, paneID)
}
