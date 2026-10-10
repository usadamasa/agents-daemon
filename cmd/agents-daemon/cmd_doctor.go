package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/backend"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newDoctorCmd は `agents-daemon doctor` を組み立てる｡
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "daemon が動作するための前提条件を診断する",
		Long: "多重化ツール (herdr / rex) の選択と到達性､Claude pane の見え方､statusline state の鮮度､\n" +
			"設定ファイルの状態､daemon の稼働状況､解決済みパスを順に確認して報告する｡多重化ツールは\n" +
			"REX_SESSION があれば rex､無ければ herdr を選ぶ｡その server へ到達できない場合のみ実際に\n" +
			"壊れているとみなし exit 1 にする｡他の項目は情報提供のみで exit 0 (state ファイルがまだ無い､\n" +
			"daemon が起動していない､daemon の多重化ツールが今の環境と違う等)｡",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			kind := backend.Detect(os.Getenv)
			client, err := backend.New(kind, sessionstate.New(p.StateDir()))
			if err != nil {
				return err
			}
			return verify.RunDoctor(cmd.Context(), cmd.OutOrStdout(), client, kind, p, time.Now())
		},
	}
}
