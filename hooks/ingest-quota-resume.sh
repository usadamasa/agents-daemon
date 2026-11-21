#!/bin/bash
# Notification hook (quota_auto_resume_*): `agents-daemon ingest-quota-resume` に stdin をそのまま渡す。
#
# Claude Code 自身の上限待ちの結末を XDG state の quota-resume/ に記録する (#529、quotaresume)。daemon は待機明けに
# stale の通知を Enter だけを送る根拠に足す。どの通知を記録するかはバイナリ側が持ち、hooks.json の matcher が
# quota_auto_resume_* に絞る。この hook が担うのはバイナリの場所の解決だけで、無ければ何もしない
# (ensure-daemon.sh が build する)。stamp が plugin の version より古いときも、理由を stderr に出して何もしない
# (daemon_bin_serves)。新しいときは通す。
#
# バイナリは stdout に何も出さず、失敗しても exit 0 で終わる (理由は stderr)。記録の失敗でセッションの作業に
# 割り込まないため。

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="ingest-quota-resume"

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

exec "$DAEMON_BIN" ingest-quota-resume
