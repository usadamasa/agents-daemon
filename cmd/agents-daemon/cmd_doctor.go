package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/backend"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newDoctorCmd は `agents-daemon doctor` を組み立てる。
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "daemon が動作するための前提条件を診断する",
		Long: "多重化ツール (rex) の到達性、Claude pane の見え方、rex CLI の応答の形 (自分の block に固定の\n" +
			"コマンドを打ち、コードが読む欄と exit code が変わっていないか)、statusline state の鮮度、\n" +
			"設定ファイルの状態、daemon の稼働状況、peer messaging の socket (CLAUDE_CODE_MESSAGING_SOCKET)、\n" +
			"解決済みパスを順に確認して報告する。rex の外 (REX_SESSION なし) と、rex の server へ\n" +
			"到達できない場合のみ実際に壊れているとみなし exit 1 にする。他の項目は情報提供のみで\n" +
			"exit 0 (state ファイルがまだ無い、daemon が起動していない等)。",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			kind, client, err := backend.Connect(os.Getenv, sessioncontext.New(p.StateDir()))
			if err != nil {
				return err
			}
			// 点検の口は Client とは別の interface。持たない実装 (fake) では nil になり、節の中身を省く。
			premises, _ := client.(multiplexer.PremiseChecker)
			return verify.RunDoctor(cmd.Context(), cmd.OutOrStdout(), verify.DoctorInput{
				Client: client, Kind: kind, Paths: p, Now: time.Now(), Getenv: os.Getenv, Premises: premises,
			})
		},
	}
}
