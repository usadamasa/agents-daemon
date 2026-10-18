package boardcli

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/boardlog"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// newBoardLogCmd は `agents-daemon board-log` を組み立てる。mod (hooks/log.ts) はファイルを直接書けないので、
// 出来事をこれに渡して、開いたセッションの domain (status --board と同じ規則) のボードのログに書かせる。
func newBoardLogCmd() *cobra.Command {
	var session, cwd string
	cmd := &cobra.Command{
		Use:   "board-log [flags] -- <event> [key=value...]",
		Short: "mod の出来事をボードの経路のログに 1 行書く",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			if cwd == "" {
				if cwd, err = os.Getwd(); err != nil {
					return err
				}
			}
			d := tasklog.LoadPlacer(p, os.Getenv("HOME")).Domain(session, cwd)
			return boardlog.Record(p.StateDir(), d, session, args[0], args[1:], time.Now())
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "出来事の起きたセッションの ID。記録が業務の側にあれば業務のログに書く")
	cmd.Flags().StringVar(&cwd, "cwd", "", "そのセッションの cwd。domain をここから決める (既定は今の cwd)")
	return cmd
}
