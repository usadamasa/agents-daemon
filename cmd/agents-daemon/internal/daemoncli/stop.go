package daemoncli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
)

// newStopCmd は `agents-daemon stop` を組み立てる。--json は daemonstate.StopResult を出す。
func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "daemon を停止する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			res, err := daemonstate.Stop(p, daemonstate.StopTimeout)
			if err != nil {
				return err
			}
			if jsonflag.Enabled(cmd) {
				return jsonflag.Write(cmd.OutOrStdout(), res)
			}
			daemonstate.WriteStop(cmd.OutOrStdout(), res)
			return nil
		},
	}
}
