#!/bin/bash
# PostToolUse hook (TaskCreate / TaskUpdate / TaskList / TodoWrite): `agents-daemon ingest-task` に stdin をそのまま渡す。
#
# セッションのタスクを XDG state の tasks/ に記録する。JSON の読み取り、domain の判定、書き込みは
# バイナリ側 (internal/tasklog、cmd_ingest_task.go) が持つ。この hook が担うのはバイナリの場所の解決だけで、
# 無ければ何もしない (ensure-daemon.sh が build する)。stamp が plugin の version と違うときも何もしない。
# plugin の更新直後は旧版のバイナリが残っていて、このサブコマンドを知らない版なら `unknown command` になる。
#
# stdout は Claude Code が読むので何も出さない (バイナリの出力も stderr へ寄せる)。
# cc-premise: 2.1.302 hooks PostToolUse の matcher に Task 系ツールの名前が当たり、stdin に tool_input / tool_response が載る

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

exec "$DAEMON_BIN" ingest-task >&2
