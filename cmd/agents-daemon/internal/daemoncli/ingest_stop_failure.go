package daemoncli

import (
	"github.com/spf13/cobra"
)

func newIngestStopFailureCmd() *cobra.Command {
	return &cobra.Command{Use: "ingest-stop-failure", RunE: func(*cobra.Command, []string) error { return nil }}
}
