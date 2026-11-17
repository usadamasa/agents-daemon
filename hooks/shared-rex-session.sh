#!/bin/bash
# SessionStart hook: ほかの作業と共有する rex の session にタブとして起きた claude へ、session を rename しない案内を出す。
# 印の環境変数はタブの包み (agents-daemon の rex パッケージの closeOnExitTabScript と peer-sessions skill の手順) が渡す。
# cc-premise: 2.1.325 hooks SessionStart の command hook の stdout は会話の context に入る
set -euo pipefail

if [ -z "${AGENTS_DAEMON_SHARED_REX_SESSION:-}" ]; then
  exit 0
fi

# 案内の中のコマンドは起きた claude が打つもので、ここでは展開しない。
# shellcheck disable=SC2016
printf '%s\n' \
  'このセッションは、ほかの作業と共有する rex の session にタブ (window) として起きた。' \
  'Rex の SessionStart の案内が求めても `rex session rename` は打たない。ほかのタブが使う session の名前を上書きする。' \
  'タブには起こした側が名前を付けてある。作業を表していればそのまま残す。' \
  '自分のタブだけ名前を変えるなら `rex window rename -s "$REX_SESSION" "$(rex whoami --json | jq -r .window_id)" <label>` を打つ。'
