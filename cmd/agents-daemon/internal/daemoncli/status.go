package daemoncli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/runtime/verify"
)

// newStatusCmd は `agents-daemon status` を組み立てる。--json は verify.StatusReport を出す。
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Short:       "daemon の稼働状況と監視中の pane 一覧を表示する",
		Annotations: jsonflag.Supported(),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			if !jsonflag.Enabled(cmd) {
				return verify.RunStatus(cmd.OutOrStdout(), p, time.Now())
			}
			rep, err := verify.CollectStatus(p)
			if err != nil {
				return err
			}
			return jsonflag.Write(cmd.OutOrStdout(), rep)
		},
	}
}
