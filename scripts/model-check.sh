#!/bin/bash
set -euo pipefail
# model-check.sh
# models/ の TLA+ のモデルを TLC で全部回す。`task model-check` が呼ぶ。
# cfg ごとに期待 (holds: 反例なし / violated: 反例が出る) を持ち、期待と違えば exit 1 にする。
# 設計を変えるときは、先にモデルへ写して holds のまま通ることを見てから実装する (models/handoff/README.md)。
#
# TLC は標準モジュールを java.io.tmpdir に展開し、探索の状態を -metadir に書く。どちらも ./tmp 配下にする
# (sandbox の中では /var/folders に書けない)。TLC の exit code は 0 が反例なし、12 が不変条件の違反。

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
  tlc -metadir "$tmp/states" -config "$dir/$cfg" "$dir/$module.tla" >"$log" 2>&1 || rc=$?
  case "$expect:$rc" in
    holds:0)
      printf '    反例なし (%s)\n' "$(awk '/distinct states found/ {print $1, "states,", $4, "distinct"}' "$log")"
      ;;
    violated:12)
      printf '    反例あり (%s、trace は %s)\n' "$(awk '/^Error: Invariant/ {print $3}' "$log")" "$log"
      ;;
    *)
      printf '    NG: exit %s (期待 %s)。log: %s\n' "$rc" "$expect" "$log"
      failed=1
      ;;
  esac
}

run models/handoff Handoff Handoff.cfg holds
run models/handoff TaskLog TaskLog.cfg holds
run models/handoff FileNotice FileNotice.cfg holds
# told を読んでから別のステップで書くと、並列の Read が同じメモを 2 回添える (AtMostOnce の反例)。
run models/handoff FileNotice FileNoticeRace.cfg violated
# compact の後も told が残ると、compact の前に伝えたメモを model が忘れたまま伝え直さない (Informed の反例)。
run models/handoff FileNotice FileNoticeCompact.cfg violated
# compact で told を空にすれば、compact の後に触ったファイルのメモは伝え直す。
run models/handoff FileNotice FileNoticeReset.cfg holds

exit "$failed"
