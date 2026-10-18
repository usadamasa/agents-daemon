#!/bin/bash
# PostToolUse hook (matcher: TaskCreate|TaskUpdate|TaskList|TodoWrite): `agents-daemon ingest-task` に stdin を
# そのまま渡す。
#
# セッションのタスクを XDG state の tasks/ に記録する (#121、internal/tasklog)。hook 入力の読み取り、私用と業務の
# 判定、書き込みはバイナリ側が持つ。この hook が担うのはバイナリの場所の解決だけで、無ければ何もしない
# (ensure-daemon.sh が build する)。stamp が plugin の version と違うときも何もしない。旧版のバイナリは
# このサブコマンドを知らず、`unknown command` で exit 1 になるため。
#
# バイナリは stdout に何も出さず、失敗しても exit 0 で終わる (理由は stderr)。記録の失敗でセッションの作業に
# 割り込まないため。

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="ingest-task"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"

[ -x "$DAEMON_BIN" ] || exit 0
[ -f "$DAEMON_STAMP" ] || exit 0
if ! version=$(plugin_version); then
  log_error "cannot read version from $PLUGIN_ROOT/.claude-plugin/plugin.json"
  exit 1
fi
[ "$(<"$DAEMON_STAMP")" = "$version" ] || exit 0

exec "$DAEMON_BIN" ingest-task
