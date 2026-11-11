#!/bin/bash
set -euo pipefail
# model-check.sh
# models/ の TLA+ のモデルを TLC で全部回す。`task model-check` が呼ぶ。
# cfg ごとに期待 (holds: 反例なし / violated: 反例が出る) を持ち、期待と違えば exit 1 にする。
# 設計を変えるときは、先にモデルへ写して holds のまま通ることを見てから実装する (models/handoff/README.md)。
#
# TLC は標準モジュールを java.io.tmpdir に展開し、探索の状態を -metadir に書く。どちらも ./tmp 配下にする
# (sandbox の中では /var/folders に書けない)。TLC の exit code は 0 が反例なし、12 が不変条件の違反、13 が
# liveness の性質 (PROPERTIES) の違反。

cd "$(dirname "$0")/.."
tmp="$PWD/tmp/tlc"
mkdir -p "$tmp"
export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djava.io.tmpdir=$tmp"

failed=0

# run <dir> <module> <cfg> <holds|violated>
run() {
  local dir=$1 module=$2 cfg=$3 expect=$4 rc=0
  local log="$tmp/$cfg.log"
  printf '==> %s/%s (%s)\n' "$dir" "$cfg" "$expect"
  # TLC は metadir の下に秒単位の時刻の名前でディレクトリを作るので、cfg ごとに分けて同じ秒の衝突を避ける。
  tlc -metadir "$tmp/states/$cfg" -config "$dir/$cfg" "$dir/$module.tla" >"$log" 2>&1 || rc=$?
  case "$expect:$rc" in
    holds:0)
      printf '    反例なし (%s)\n' "$(awk '/^[0-9]+ states generated/ {print $1, "states,", $4, "distinct"}' "$log")"
      ;;
    violated:12)
      printf '    反例あり (%s、trace は %s)\n' "$(awk '/^Error: Invariant/ {print $3}' "$log")" "$log"
      ;;
    violated:13)
      printf '    反例あり (liveness、trace は %s)\n' "$log"
      ;;
    *)
      printf '    NG: exit %s (期待 %s)。log: %s\n' "$rc" "$expect" "$log"
      failed=1
      ;;
  esac
}

run models/handoff Handoff Handoff.cfg holds
# 取るのと宛先を書くのを別の park set にすると、宛先の無い handed が見える (HandedHasDestination の反例)。
run models/handoff Handoff HandoffSplit.cfg violated
run models/handoff TaskLog TaskLog.cfg holds
# 選ぶのと told に足すのを 1 回の update で行い、compact で told を空にする形 (hooks/notice.ts)。
run models/handoff FileNotice FileNotice.cfg holds
# told を読んでから別のステップで書くと、並列の Read が同じメモを 2 回添える (AtMostOnce の反例)。
run models/handoff FileNotice FileNoticeRace.cfg violated
# compact の後も told が残ると、compact の前に伝えたメモを model が忘れたまま伝え直さない (Informed の反例)。
run models/handoff FileNotice FileNoticeCompact.cfg violated
# 結びつけと edge add / edge rm を edges/.lock の中で読んで書く形 (edge パッケージの Bind、Add、Remove)。
run models/handoff EdgeBind EdgeBind.cfg holds
# lock 無しで edge add が読んでから書くと、間に結びついた端点を名前に戻す (BindOnce の反例)。
run models/handoff EdgeBind EdgeBindRaceAdd.cfg violated
# lock 無しで結びつけが読んでから書くと、間に edge rm で消した辺を作り直す (NoResurrect の反例)。
run models/handoff EdgeBind EdgeBindRaceRemove.cfg violated
# hook と companion が work/.lock の中で読んでから自分の欄だけを書く形 (worklog)。liveness も見る。
run models/work Work Work.cfg holds
# lock 無しで読んだ写しを書くと、人が直した subject を hook が戻す (HumanSubjectKept の反例)。
run models/work Work WorkRace.cfg violated
# 同じく、SessionEnd が書いた ended を遅れた Stop や companion が消す (EndedStays の反例)。
run models/work Work WorkRaceEnded.cfg violated
# 同じく、UserPromptSubmit と Stop が交錯して turns の更新を失う (NoLostUpdate の反例)。
run models/work Work WorkRaceTurns.cfg violated
# 外部の参照の状態を daemon が gh で引いて写しに書き、依存図が merged / closed を完了にする形 (#416)。
run models/refstate RefState RefState.cfg holds
# gh の失敗を closed として写しに書くと、済んでいない参照を完了と見せる (NoFalseComplete の反例)。
run models/refstate RefState RefStateFailAsClosed.cfg violated
# マージせずに閉じた PR、not planned で閉じた issue を済んだ状態と書くと、届いていない参照を完了と見せる (NoFalseComplete の反例)。
run models/refstate RefState RefStateDroppedAsDone.cfg violated
# 写しが open の参照を引き直さないと、マージされた PR がいつまでも完了にならない (EventuallyCompleted の反例)。
run models/refstate RefState RefStateNoRefetch.cfg violated
# 済んだ状態に変わった引き直しのときだけ辺を消すと、済んだ PR へ後から書いた辺が残り続ける (EdgeRemoved の反例、#422)。
run models/refstate RefState RefStateSweepOnTransition.cfg violated
# rex の全列挙で block ls の exit 3 を、ls の後に閉じた session として読み飛ばす形 (#302)。
run models/rexlist PaneList PaneList.cfg holds
# exit 3 を全列挙の失敗にすると、session を閉じただけで失敗する (FailOnlyWhenDown の反例)。
run models/rexlist PaneList PaneListNoSkip.cfg violated
# 終了したセッションの完了タスクを作業の記録が消えるまで掃除しない形 (#483)。カードの状態を実装の式で導く。liveness も見る。
run models/card Card Card.cfg holds
# 完了タスクを 24 時間で消すと、全部終えて終了したセッションが終了のカードで図に戻る (NoResurrectedExited の反例)。
run models/card Card CardNoKeep.cfg violated
# 同じく、そのセッションを待つ辺の上流が完了から終了へ変わる (SatisfiedStays の反例)。
run models/card Card CardNoKeepEdge.cfg violated
# tick で mtime を進めないと、生きているセッションの未完了のタスクが 7 日で消える (LiveTaskKept の反例)。
run models/card Card CardNoTouchTask.cfg violated
# 同じく、生きているセッションの辺が 7 日で消える (LiveEdgeKept の反例)。
run models/card Card CardNoTouchEdge.cfg violated
# 同じく、open の PR の辺が 7 日で消える (OpenRefEdgeKept の反例)。
run models/card Card CardNoTouchRef.cfg violated
# 辺の note の #N を、番号の合う端点の参照の repo で補う形 (#551)。合わなければ辺が触れる repo が 1 つのときだけ補う。
# 参照の repo は ref の文字列から取り、状態を引けたかに依らない。
run models/noterepo NoteRepo NoteRepo.cfg holds
# after の端点の repo で補うと、別 repo の PR を待つ辺で #N が after の repo を指す (RefNoteResolves の反例)。
run models/noterepo NoteRepo NoteRepoAfter.cfg violated
# before の端点で補うと、after の参照の番号を書いた note (skill の session → pr の例) が before の repo を指す (RefNoteResolves の反例)。
run models/noterepo NoteRepo NoteRepoBefore.cfg violated
# 両端の参照が同じ番号で別 repo のとき before を選ぶと、after を指した note を before の repo へ送る (NoWrongLink の反例)。
run models/noterepo NoteRepo NoteRepoPreferBefore.cfg violated
# 番号の合わない #N を before の端点の repo で補うと、書き手の repo の issue を指した note を別 repo へ送る (NoWrongUnmatchedLink の反例)。
run models/noterepo NoteRepo NoteRepoFallbackBefore.cfg violated
# 参照の repo を daemon が引いた状態から取ると、状態を引けない PR の番号を書いた note がリンクにならない (RefNoteResolves の反例)。
run models/noterepo NoteRepo NoteRepoViaState.cfg violated

exit "$failed"
