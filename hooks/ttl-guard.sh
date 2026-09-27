#!/bin/bash
# UserPromptSubmit hook: prompt cache の失効後の最初の prompt を 1 回だけ止める｡
#
# hook がするのは stdin をバイナリの `ttl-guard` へ渡し､止めるか (exit 2) を伝えることだけ｡
# transcript の読み取りと ack の規則は Go 側 (internal/sessionstate) が持つ｡
#
# fail-open: バイナリが無い・失敗した (サブコマンドを持たない古いバイナリも含む) ときは exit 0｡
# exit 0 の stdout は context に入るため､バイナリの stdout も stderr へ寄せる｡

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="ttl-guard"

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
"$DAEMON_BIN" ttl-guard >&2 || rc=$?
if [ "$rc" -eq 2 ]; then
  exit 2
fi
if [ "$rc" -ne 0 ]; then
  log_error "ttl-guard failed (rc=$rc): $DAEMON_BIN"
fi

exit 0
