#!/bin/bash
# SessionStart hook: ほかの作業と共有する rex の session にタブとして起きた claude へ、session を rename しない案内を出す。
# 印の環境変数はタブの包み (agents-daemon の rex/wrap パッケージの tabScript) が渡す。
# 起こし方ごとの rename の扱いは agents-daemon の spawn パッケージの doc が持つ。
# cc-premise: 2.1.325 hooks SessionStart の command hook の stdout は会話の context に入る
# rex-premise: preview.37 cli rex の block の中では -s を省くと自分の block の居る session を指し、whoami は window_id を返す
set -euo pipefail

if [ -z "${AGENTS_DAEMON_SHARED_REX_SESSION:-}" ]; then
  exit 0
fi

# 案内の中のコマンドは起きた claude が打つもので、ここでは展開しない。
# shellcheck disable=SC2016
printf '%s\n' \
  'このセッションは、ほかの作業と共有する rex の session にタブ (window) として起きた。' \
  'Rex の SessionStart の案内が求めても `rex session rename` は打たない。ほかのタブが使う session の名前を上書きする。' \
  'タブの名前は agents-daemon が作業の内容に合わせて付け直すので、名前を付けるために打つものは無い。' \
  'すぐ変えたいときは自分のタブだけ `rex window rename "$(rex whoami --json | jq -r .window_id)" <label>` で変える。'
