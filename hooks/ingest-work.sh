#!/bin/bash
# SessionStart・UserPromptSubmit・Stop・SessionEnd hook: `agents-daemon ingest-work` に stdin をそのまま渡す。
#
# セッション 1 つの作業の記録 (最初の prompt の題、最後のターンの要約、turns、始まりと終わり) を XDG state の work/ に
# 書く (#368、worklog)。どの event で何を書くかはバイナリ側が持つ。この hook が担うのはバイナリの場所の解決だけで、
# 無ければ何もしない (ensure-daemon.sh が build する)。stamp が plugin の version より古いときも、理由を stderr に出して
# 何もしない (daemon_bin_serves)。新しいときは通す。
#
# バイナリは stdout に何も出さず (SessionStart と UserPromptSubmit の stdout は会話の context に入る)、失敗しても exit 0 で
# 終わる (理由は stderr)。記録の失敗でセッションの作業に割り込まないため。SessionEnd の予算 (1.5 秒) に対してバイナリは
# 数 ms で返る (実測 8 ms)。

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="ingest-work"

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
daemon_bin_serves "$(<"$DAEMON_STAMP")" "$version" || exit 0

exec "$DAEMON_BIN" ingest-work
