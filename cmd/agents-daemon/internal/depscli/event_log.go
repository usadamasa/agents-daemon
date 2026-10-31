package depscli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// newEventLogCmd は `agents-daemon event-log` を組み立てる。mod (hooks/log.ts) はファイルを直接書けないので、
// 出来事をこれに渡して出来事のログに書かせる。stdout に何も出さないので --json には対応しない。
func newEventLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "event-log",
		Short:       "stdin の EventLogRequest で mod の出来事を出来事のログに 1 行書く",
		Args:        cobra.NoArgs,
		Annotations: jsonflag.Unsupported(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			var req protocol.EventLogRequest
			if err := protocol.Decode(cmd.InOrStdin(), "EventLogRequest", &req); err != nil {
				return err
			}
			fields := make([]string, 0, len(req.Fields))
			for _, f := range req.Fields {
				fields = append(fields, f.Key+"="+f.Value)
			}
			return eventlog.Record(p.StateDir(), req.SessionID, req.Event, fields, time.Now())
		},
	}
}
