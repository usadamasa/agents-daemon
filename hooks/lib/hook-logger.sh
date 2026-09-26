#!/bin/bash
set -euo pipefail
# hook-logger.sh
# hook スクリプト共通のログヘルパー｡出力は常に stderr (hook の stdout は Claude Code が読むため)｡
#
# 使い方:
#   HOOK_NAME="compaction-recovery"
#   source "$SCRIPT_DIR/lib/hook-logger.sh"
#
# 環境変数:
#   HOOK_NAME  ログプレフィックスに使用するスクリプト名

HOOK_NAME="${HOOK_NAME:-hook}"

# プレフィックスは source 時に一度だけ確定させる (HOOK_NAME は source 前に設定される前提)｡
_LOG_PREFIX="[$HOOK_NAME]"

# スキップ理由の通知
log_skip() {
  local reason="$1"
  printf '%s SKIP   (%s)\n' "$_LOG_PREFIX" "$reason" >&2
}

# 情報メッセージ (常に出力)
log_info() {
  local msg="$1"
  printf '%s INFO   %s\n' "$_LOG_PREFIX" "$msg" >&2
}

# エラーメッセージ (常に出力、重大度: ERROR)
log_error() {
  local msg="$1"
  printf '%s ERROR  %s\n' "$_LOG_PREFIX" "$msg" >&2
}
