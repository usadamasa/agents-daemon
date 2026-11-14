package parkcli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
)

// newSpawnedSessionCmd は `agents-daemon spawned-session mark|close <session_id>` を組み立てる (spawnedsession)。
// mark は spawn と peer-sessions skill の手順が起こした rex session の中で、包みの sh がコマンドを起こす前に呼ぶ。
// close は起こした側が、控えた session_id で peer の session を畳むときに呼ぶ。
func newSpawnedSessionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "spawned-session", Short: "起こした rex session の印を扱う (pane が空になったら daemon が閉じる。印のある session は close で閉じられる)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "mark <session_id>",
		Short: "rex session の印を書き、書いた印を JSON で出す",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return statecmd.Run(cmd, func(p apppath.Paths, _ io.Reader) (any, error) {
				id := args[0]
				if id == "" {
					return nil, errors.New("session ID が空")
				}
				store := spawnedsession.New(p.StateDir())
				if err := store.Put(id); err != nil {
					return nil, err
				}
				rec, ok := store.Get(id)
				if !ok {
					return nil, fmt.Errorf("書いた印 %s を読めない", id)
				}
				return rec, nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "close <session_id>",
		Short: "印の付いた rex session を閉じて印を消し、結果を JSON で出す。印の無い session は閉じない",
		Long: "peer を起こした側が、`rex session new --json` の session_id で peer の session を畳むときに呼ぶ。\n" +
			"label は起きた claude が rename するので宛先に使わない。session がもう rex に無ければ、印の有無にかかわらず\n" +
			"killed:false の成功にして印を消す。印 (spawned-session mark) の無い session が rex に居れば閉じずに error にする。\n" +
			"閉じられなければ印を残して error にする。",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return statecmd.Run(cmd, func(p apppath.Paths, _ io.Reader) (any, error) {
				store := spawnedsession.New(p.StateDir())
				if args[0] == "" {
					return nil, errors.New("session ID が空")
				}
				closer, ok := backend.ConnectRex(sessioncontext.New(p.StateDir())).(sessionCloser)
				if !ok {
					return nil, errors.New("この backend は session を閉じられない")
				}
				return closeSpawnedSession(cmd.Context(), store, closer, args[0])
			})
		},
	})
	return cmd
}

// sessionCloser は close が使う多重化ツールの操作。rex の backend が満たす。
type sessionCloser interface {
	SessionBlocks(ctx context.Context, sessionID string) (n int, found bool, err error)
	SessionKill(ctx context.Context, sessionID string) error
}

// closeResult は close の結果。Killed は session を閉じたか (false はもう無かった)。
type closeResult struct {
	SessionID string `json:"session_id"`
	Killed    bool   `json:"killed"`
}

// closeSpawnedSession は印の付いた session を閉じて印を消す。session がもう無ければ、印の有無にかかわらず
// killed=false の成功にする。reaper は session が無くなると印を消し、閉じた理由は残さないので、印が無いだけでは
// 起こしていない session と畳まれた session を見分けられない。印の無い session が rex に居れば閉じずに拒否する。
// 数えられない、閉じられないときは印を残し、daemon の reaper が後で拾う。
func closeSpawnedSession(ctx context.Context, store spawnedsession.Store, closer sessionCloser, id string) (closeResult, error) {
	if id == "" {
		return closeResult{}, errors.New("session ID が空")
	}
	_, marked := store.Get(id)
	_, found, err := closer.SessionBlocks(ctx, id)
	switch {
	case err != nil && !marked:
		return closeResult{}, fmt.Errorf("session %s には起こした印が無く、rex に残っていないかを確かめられない: %w", id, err)
	case err != nil:
		return closeResult{}, fmt.Errorf("session %s を確かめられない: %w", id, err)
	case found && !marked:
		return closeResult{}, fmt.Errorf("session %s には起こした印が無いので閉じない (spawned-session mark が書いた session だけを閉じる)", id)
	}
	killed := false
	if found {
		killed, err = killOrGone(ctx, closer, id)
		if err != nil {
			return closeResult{}, err
		}
	}
	if err := store.Remove(id); err != nil {
		return closeResult{}, fmt.Errorf("session %s の印を消せない: %w", id, err)
	}
	return closeResult{SessionID: id, Killed: killed}, nil
}

// killOrGone は session を閉じる。閉じられなくても、数え直して session が無ければ (数えた後に包みの c() か reaper が
// 先に閉じた) killed=false の成功にする。
func killOrGone(ctx context.Context, closer sessionCloser, id string) (killed bool, err error) {
	killErr := closer.SessionKill(ctx, id)
	if killErr == nil {
		return true, nil
	}
	if _, found, err := closer.SessionBlocks(ctx, id); err == nil && !found {
		return false, nil
	}
	return false, fmt.Errorf("session %s を閉じられない: %w", id, killErr)
}
