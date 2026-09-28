// agents-daemon は Claude Code が 5 時間ウィンドウの利用上限で停止したとき､
// 上限が解除された時刻に自動でプロンプトを送って作業を再開するツール｡
// context の使用率が閾値を超えた pane へ /compact を投入する役も担う｡
//
// サブコマンド:
//
//	daemon    監視デーモン (--ensure で detach 起動､--foreground で直接ループ)
//	status    daemon の稼働状況と監視中の pane 一覧の表示
//	logs      直近のログの表示
//	stop      daemon の停止
//	doctor    前提条件の診断
//	inspect   画面テキストの分類結果と判定根拠の表示
//	simulate  使い捨て pane を使った end-to-end 検証
//	ingest-statusline  statusline の stdin から context/ と rate-limits/ の state file を書く
//	ingest-stop        Stop hook の stdin から cache/ の state file を書く
//	ttl-guard          UserPromptSubmit hook の stdin を見て､cache 失効後の最初の prompt を止める
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		if ec, ok := errors.AsType[exitCodeError](err); ok {
			os.Exit(ec.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
