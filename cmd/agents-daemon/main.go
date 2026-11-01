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
//	update    plugin の更新と、更新した版での daemon の建て直し
//	doctor    前提条件の診断
//	inspect   画面テキストの分類結果と判定根拠の表示
//	simulate  使い捨て pane を使った end-to-end 検証
//	statusline         Claude Code の statusline を描画し、同じプロセスで state file と cost の記録を書く
//	ingest-statusline  statusline の stdin から context/ と rate-limits/ の state file を書く (自前の statusline から呼ぶ)
//	ingest-task  PostToolUse hook の stdin からタスクを tasks/ に記録する
//	ingest-turn-failure  StopFailure hook の stdin から turn を終わらせた API error を turn-failures/ に記録する (Stop と UserPromptSubmit で消す)
//	park      パーキングロットのメモを parking/ に置く・書き換える・一覧する (add / set / list)
//	dream     記憶階層の consolidation の実行系 (harvest / mine / stage / adopt / forget)。手順は agents-daemon:dream skill
//	prune-worktrees  誰も使っていない Claude Code の worktree の回収 (SessionStart hook が --hook 付きで呼ぶ)
//	version   建てた元の plugin の version の表示 (--version も同じ)
//
// グローバルオプション --json は出力を JSON にする。hook 用のコマンドと update は対応しない (jsonflag)。
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// stderr が daemon.log に向くこと (detach 起動した daemon) があるので、誰の行か分かる形にする。
		fmt.Fprintf(os.Stderr, "agents-daemon: %v\n", err)
		os.Exit(1)
	}
}
