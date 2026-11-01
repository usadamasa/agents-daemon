#!/bin/bash
# StopFailure・Stop・UserPromptSubmit hook: `agents-daemon ingest-turn-failure` に stdin をそのまま渡す。
#
# turn を終わらせた API error を XDG state の turn-failures/ に記録し、次の turn で消す (#77、turnfailure)。
# どの event で書くか消すかはバイナリ側が持つ。この hook が担うのはバイナリの場所の解決だけで、無ければ何もしない
# (ensure-daemon.sh が build する)。stamp が plugin の version と違うときも何もしない。旧版のバイナリは
# このサブコマンドを知らず、`unknown command` で exit 1 になるため。
#
# バイナリは stdout に何も出さず (UserPromptSubmit の stdout は会話の context に入る)、失敗しても exit 0 で
# 終わる (理由は stderr)。記録の失敗でセッションの作業に割り込まないため。

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="ingest-turn-failure"

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

exec "$DAEMON_BIN" ingest-turn-failure
