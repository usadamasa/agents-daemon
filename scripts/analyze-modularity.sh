#!/bin/bash
set -euo pipefail
# analyze-modularity.sh
# `go tool analyze-modularity --strict` を、base の commit で測ったレポートと比べて回す。`task lint` が呼ぶ。
# 揮発の窓は判定する変更自身の commit も含むので、base から D を悪くしたパッケージだけを落とす。
#
# BASE_SHA は比べる先。CI は pull_request の base.sha か push の before を渡す。
# 未設定なら origin/main との merge-base と比べる。空かすべて 0 (新しいブランチの最初の push) なら比べない。
# base も今の checkout の go.mod が固定する同じバイナリで測る (go tool を今の checkout を cwd にして呼ぶ)。

cd "$(dirname "$0")/.."

if [ -z "${BASE_SHA+x}" ]; then
  BASE_SHA=$(git merge-base HEAD origin/main)
fi
readonly BASE_SHA

case "$BASE_SHA" in
  "" | 0000000*)
    printf '%s\n' "analyze-modularity: 比べる base が無いので --baseline なしで判定する" >&2
    exec go tool analyze-modularity --strict .
    ;;
esac

work=$(mktemp -d)
readonly work
cleanup() {
  if ! git worktree remove --force "$work/base"; then
    printf '%s\n' "analyze-modularity: base の worktree を消せなかった: $work/base" >&2
  fi
  rm -rf "$work"
}
trap cleanup EXIT

git worktree add --quiet --detach "$work/base" "$BASE_SHA"
go tool analyze-modularity "$work/base" > "$work/base.json"
go tool analyze-modularity --strict --baseline "$work/base.json" .
