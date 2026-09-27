#!/bin/bash
# Stop hook: transcript から prompt cache の状態を cache/<session_id>.json へ書かせる｡
#
# hook がするのは stdin をバイナリの `ingest-stop` へ渡すことだけ｡transcript の読み取りと
# sidecar の書式は Go 側 (internal/sessionstate) が持つ｡
#
# fail-open: バイナリが無い (SessionStart の build 前) でも失敗しても exit 0 で戻る｡
# Stop hook の stdout は Claude Code が判定 JSON として読むため､バイナリの stdout も stderr へ寄せる｡

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="cache-state"

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

if ! "$DAEMON_BIN" ingest-stop >&2; then
  log_error "ingest-stop failed: $DAEMON_BIN"
fi

exit 0
