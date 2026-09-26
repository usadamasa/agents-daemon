package main

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newInspectCmd は `agents-daemon inspect` を組み立てる。
func newInspectCmd() *cobra.Command {
	var paneID string
	var file string
	var sessionID string

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "画面テキストの分類と、待機時刻の算出根拠を表示する (送信はしない)",
		Long: "--pane <id> は herdr 越しに生きた pane を読み、--file <path> は保存済みの画面\n" +
			"テキストファイルを読む。どちらか一方を必ず指定する。\n" +
			"分類器 (detect.Classifier) を daemon (monitor パッケージ) と全く同じ設定で走らせ、\n" +
			"判定結果だけでなく、走査した行範囲・ゲート判定 (statusline の 5 時間ウィンドウ\n" +
			"使用率)・起床時刻とその根拠までを表示する。pane への送信は一切行わない。",
		RunE: func(cmd *cobra.Command, args []string) error {
			if (paneID == "") == (file == "") {
				return errors.New("--pane か --file のどちらか一方を指定してください")
			}
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			return verify.RunInspect(cmd.Context(), cmd.OutOrStdout(), herdrcli.NewExecClient(), p, paneID, file, sessionID, time.Now())
		},
	}
	cmd.Flags().StringVar(&paneID, "pane", "", "herdr 越しに読む pane ID")
	cmd.Flags().StringVar(&file, "file", "", "保存済み画面テキストファイルのパス")
	cmd.Flags().StringVar(&sessionID, "session", "", "利用上限 state を引く Claude セッション ID (--pane 指定時は pane から解決するため不要)")
	return cmd
}
