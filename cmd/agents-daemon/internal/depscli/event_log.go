package depscli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
)

// newEventLogCmd は `agents-daemon event-log` を組み立てる。mod (hooks/log.ts) はファイルを直接書けないので、
// 出来事をこれに渡して出来事のログに書かせる。
func newEventLogCmd() *cobra.Command {
	var session string
	cmd := &cobra.Command{
		Use:   "event-log [flags] -- <event> [key=value...]",
		Short: "mod の出来事を出来事のログに 1 行書く",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return eventlog.Record(p.StateDir(), session, args[0], args[1:], time.Now())
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "出来事の起きたセッションの ID")
	return cmd
}
