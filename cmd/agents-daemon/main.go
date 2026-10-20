// agents-daemon は Claude Code のセッション群のそばに常駐し、セッションの中からはできないこと
// (止まったセッションへの働きかけ、セッションを跨いだ状態の集約、時間で動く掃除、多重化ツールへの操作)
// を背後で担うデーモンと、その CLI。役割の区分と境界は README の「役割」にある。
//
// サブコマンド:
//
//	daemon    監視デーモン (--ensure で detach 起動、--foreground で直接ループ)
//	status    daemon の稼働状況と監視中の pane 一覧の表示
//	logs      直近のログの表示
//	stop      daemon の停止
//	doctor    前提条件の診断
//	inspect   画面テキストの分類結果と判定根拠の表示
//	simulate  使い捨て pane を使った end-to-end 検証
//	statusline         Claude Code の statusline を描画し、同じプロセスで state file と cost の記録を書く
//	ingest-statusline  statusline の stdin から context/ と rate-limits/ の state file を書く (自前の statusline から呼ぶ)
//	ingest-task  PostToolUse hook の stdin からタスクを tasks/ に記録する
//	park      パーキングロットのメモを parking/ に置く・書き換える・一覧する (add / set / list)
//	dream     記憶階層の consolidation の実行系 (harvest / mine / stage / adopt / forget)。手順は agents-daemon:dream skill
//	prune-worktrees  誰も使っていない Claude Code の worktree の回収 (SessionStart hook が --hook 付きで呼ぶ)
//
// グローバルオプション --json は doctor と status の出力を JSON にする (internal/jsonflag)。
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
