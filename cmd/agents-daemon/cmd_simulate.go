package main

import (
	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newSimulateCmd は `agents-daemon simulate` を組み立てる｡
func newSimulateCmd() *cobra.Command {
	var send bool
	var keep bool
	var screenFile string

	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "使い捨て pane を作り､検知から送信までを end-to-end で検証する",
		Long: "herdr pane split で自分専用の使い捨て pane を作り､herdr pane report-agent で\n" +
			"それを Claude agent に見せかけ､limit 風の画面を書き込んで､daemon と同じ\n" +
			"monitor.Tick を走らせる｡既定では dry-run (--send を付けると使い捨て pane に\n" +
			"限って実際に送信する)｡\n\n" +
			"simulate は自分が作った pane 以外には構造的に一切書き込めない (herdrcli.Client を\n" +
			"scopedClient で包み､作成した pane_id 以外への呼び出しを拒否する)｡実 Claude\n" +
			"セッションが乗った pane を壊さないための安全策で､テストでも確認している｡\n\n" +
			"証明すること: pane 列挙→Claude agent 判定→画面読取→分類→ゲート判定→送信内容の\n" +
			"決定､までを daemon と全く同じコード経路で通せること｡\n" +
			"証明しないこと: 実在の Claude セッションに対する検知の正しさ (limit 画面は cat に\n" +
			"よる疑似表示であり､本物の Claude Code が描くものではない) や､実際に Claude Code へ\n" +
			"送信したときの受理・再開挙動 (--send でも相手は素の shell pane)｡",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return verify.RunSimulate(cmd.Context(), cmd.OutOrStdout(), p, verify.SimulateOptions{Send: send, Keep: keep, ScreenFile: screenFile})
		},
	}
	cmd.Flags().BoolVar(&send, "send", false, "dry-run をやめ､使い捨て pane へ実際に再開メッセージを送信する")
	cmd.Flags().BoolVar(&keep, "keep", false, "終了後も使い捨て pane を閉じずに残す (手動確認用)")
	cmd.Flags().StringVar(&screenFile, "screen-file", "", "書き込む画面テキストファイル (省略時は内蔵の limit サンプルを使う)")
	return cmd
}
