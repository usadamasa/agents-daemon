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
			"label は起きた claude が rename するので宛先に使わない。印 (spawned-session mark) の無い session は\n" +
			"rex に触れずに error にする。session がもう無ければ印だけ消す。閉じられなければ印を残して error にする。",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return statecmd.Run(cmd, func(p apppath.Paths, _ io.Reader) (any, error) {
				store := spawnedsession.New(p.StateDir())
				if err := requireMark(store, args[0]); err != nil {
					return nil, err
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

// requireMark は印の有無で閉じてよい session かを決める。起こしていない session を閉じないための線。
func requireMark(store spawnedsession.Store, id string) error {
	if id == "" {
		return errors.New("session ID が空")
	}
	if _, ok := store.Get(id); !ok {
		return fmt.Errorf("session %s には起こした印が無いので閉じない (spawned-session mark が書いた session だけを閉じる)", id)
	}
	return nil
}

// closeSpawnedSession は印の付いた session を閉じて印を消す。session がもう無ければ印だけ消す。数えられない、
// 閉じられないときは印を残し、daemon の reaper が後で拾う。
func closeSpawnedSession(ctx context.Context, store spawnedsession.Store, closer sessionCloser, id string) (closeResult, error) {
	if err := requireMark(store, id); err != nil {
		return closeResult{}, err
	}
	_, found, err := closer.SessionBlocks(ctx, id)
	if err != nil {
		return closeResult{}, fmt.Errorf("session %s を確かめられない: %w", id, err)
	}
	if found {
		if err := closer.SessionKill(ctx, id); err != nil {
			return closeResult{}, fmt.Errorf("session %s を閉じられない: %w", id, err)
		}
	}
	if err := store.Remove(id); err != nil {
		return closeResult{}, fmt.Errorf("session %s の印を消せない: %w", id, err)
	}
	return closeResult{SessionID: id, Killed: found}, nil
}
