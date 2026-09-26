#!/bin/bash
# PostCompact hook: 圧縮完了を marker file で記録する｡
# context 注入は UserPromptSubmit 側 (userpromptsubmit-compaction-recovery.sh) で行う｡
#
# marker を経由する 2 段構成をここで畳まないこと｡この marker は
# agents-daemon の daemon が「圧縮が完了して、まだ次のプロンプトが
# 来ていない」を知る唯一の手がかりで、daemon はこれを見て作業の再開を促す｡
# PostCompact から直接 context を注入する形にすると、その合図が消える｡
#
# fail-open: 個別操作失敗は log_error しつつ常に exit 0 で戻る｡

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="compaction-recovery"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  echo "ERROR: [$HOOK_NAME] hook-logger.sh not found at $SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/compact-markers.sh"

SESSION_ID=$(compact_read_session_id) || SESSION_ID=""
[[ -z "$SESSION_ID" ]] && exit 0

# marker file を書く (UserPromptSubmit が検出して context 注入へ移動する)｡
# mtime は下流で使わないので touch で十分｡
if ! mkdir -p "$COMPACTED_DIR"; then
  log_error "mkdir failed: $COMPACTED_DIR"
  exit 0
fi
if ! touch "$COMPACTED_DIR/$SESSION_ID"; then
  log_error "touch failed: $COMPACTED_DIR/$SESSION_ID"
  exit 0
fi

exit 0
