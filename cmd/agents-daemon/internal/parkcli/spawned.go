package parkcli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
)

// newSpawnedSessionCmd は `agents-daemon spawned-session mark <session_id>` を組み立てる (spawnedsession)。
// spawn と peer-sessions skill の手順が起こした rex session の中で、包みの sh がコマンドを起こす前に呼ぶ。
func newSpawnedSessionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "spawned-session", Short: "起こした rex session の印を扱う (pane が空になったら daemon が閉じる)"}
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
	return cmd
}
