package boardcli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/boardlog"
)

// newBoardLogCmd は `agents-daemon board-log` を組み立てる。mod (hooks/log.ts) はファイルを直接書けないので、
// 出来事をこれに渡してボードの経路のログに書かせる。
func newBoardLogCmd() *cobra.Command {
	var session string
	cmd := &cobra.Command{
		Use:   "board-log [flags] -- <event> [key=value...]",
		Short: "mod の出来事をボードの経路のログに 1 行書く",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return boardlog.Record(p.StateDir(), session, args[0], args[1:], time.Now())
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "出来事の起きたセッションの ID")
	return cmd
}
