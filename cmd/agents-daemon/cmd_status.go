package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newStatusCmd は `agents-daemon status` を組み立てる。
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "daemon の稼働状況と監視中の pane 一覧を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return verify.RunStatus(cmd.OutOrStdout(), p, time.Now())
		},
	}
}
