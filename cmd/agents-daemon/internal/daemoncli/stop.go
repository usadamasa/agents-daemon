package daemoncli

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// newStopCmd は `agents-daemon stop` を組み立てる。
func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "daemon を停止する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return daemon.Stop(cmd.OutOrStdout(), p, daemon.StopTimeout)
		},
	}
}
