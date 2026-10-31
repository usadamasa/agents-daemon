package daemoncli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/runtime/simulate"
)

// newSimulateCmd は `agents-daemon simulate` を組み立てる。--json は simulate.Result を出す。
func newSimulateCmd() *cobra.Command {
	var send bool
	var keep bool
	var screenFile string

	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "使い捨て pane を作り、検知から送信までを end-to-end で検証する",
		Long: "自分の pane を分割して使い捨て pane を作り、それを Claude agent に見せかけ、limit 風の\n" +
			"画面を出して、daemon と同じ monitor.Tick を走らせる。既定では dry-run (--send を\n" +
			"付けると使い捨て pane に限って実際に送信する)。使い捨て pane の前景は画面を cat した\n" +
			"あと cat -v で居座る script で、送った esc・文字列・enter を解釈せずそのまま画面に書く。\n" +
			"REX_SESSION と REX_BLOCK のある rex の block 内で打つ。script が Claude Code と同じ\n" +
			"OSC 7501 を printf して program_status に載せ、Claude agent に見せる。\n\n" +
			"simulate は自分が作った pane 以外には構造的に一切書き込めない (pane を読む・送る操作を\n" +
			"scopedClient で包み、作成した pane_id 以外への呼び出しを拒否する)。実 Claude\n" +
			"セッションが乗った pane を壊さないための安全策で、テストでも確認している。\n\n" +
			"証明すること: pane 列挙→Claude agent 判定→画面読取→分類→ゲート判定→送信内容の\n" +
			"決定、までを daemon と全く同じコード経路で通せること。\n" +
			"証明しないこと: 実在の Claude セッションに対する検知の正しさ (limit 画面は cat に\n" +
			"よる疑似表示であり、本物の Claude Code が描くものではない) や、実際に Claude Code へ\n" +
			"送信したときの受理・再開挙動 (--send でも相手は cat -v で、届いた入力をそのまま見せるだけ)。",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			opts := simulate.Options{Send: send, Keep: keep, ScreenFile: screenFile}
			if !jsonflag.Enabled(cmd) {
				_, err := simulate.Run(cmd.Context(), cmd.OutOrStdout(), p, opts)
				return err
			}
			// 途中で失敗しても、そこまでに通った経路 (作った pane と後始末) は出す。
			res, err := simulate.Run(cmd.Context(), io.Discard, p, opts)
			if writeErr := jsonflag.Write(cmd.OutOrStdout(), res); writeErr != nil {
				return writeErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&send, "send", false, "dry-run をやめ、使い捨て pane へ実際に再開メッセージを送信する")
	cmd.Flags().BoolVar(&keep, "keep", false, "終了後も使い捨て pane を閉じずに残す (手動確認用)")
	cmd.Flags().StringVar(&screenFile, "screen-file", "", "書き込む画面テキストファイル (省略時は内蔵の limit サンプルを使う)")
	return cmd
}
