package main

import (
	"github.com/spf13/cobra"
)

func newParkCmd() *cobra.Command {
	return &cobra.Command{Use: "park"}
}
