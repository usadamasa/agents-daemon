#!/bin/bash
# UserPromptSubmit / Stop hook: Claude Code のセッション名とブランチを herdr の space 欄とタブ行へ写す｡
#
# hook がするのは stdin をバイナリの `herdr-title` へ渡すことだけ｡herdr の pane の外かの判定と
# socket の呼び出しは Go 側 (internal/hookreport) が持つ｡
#
# fail-open: バイナリが無い・失敗した (サブコマンドを持たない古いバイナリも含む) ときは exit 0｡
# UserPromptSubmit の exit 0 の stdout は context に入るため､バイナリの stdout も stderr へ寄せる｡

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="herdr-title"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"

if [ ! -x "$DAEMON_BIN" ]; then
  log_skip "daemon binary not found: $DAEMON_BIN"
  exit 0
fi

rc=0
"$DAEMON_BIN" herdr-title >&2 || rc=$?
if [ "$rc" -ne 0 ]; then
  log_error "herdr-title failed (rc=$rc): $DAEMON_BIN"
fi

exit 0
