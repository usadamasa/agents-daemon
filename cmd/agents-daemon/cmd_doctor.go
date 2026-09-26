package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newDoctorCmd は `agents-daemon doctor` を組み立てる。
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "daemon が動作するための前提条件を診断する",
		Long: "herdr への到達性、Claude pane の見え方、statusline state の鮮度、設定ファイルの\n" +
			"状態、daemon の稼働状況、解決済みパスを順に確認して報告する。herdr へ到達できない\n" +
			"場合のみ実際に壊れているとみなし exit 1 にする。他の項目は情報提供のみで exit 0 (state\n" +
			"ファイルがまだ無い、daemon が起動していない等は異常ではない)。",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return verify.RunDoctor(cmd.Context(), cmd.OutOrStdout(), herdrcli.NewExecClient(), p, time.Now())
		},
	}
}
