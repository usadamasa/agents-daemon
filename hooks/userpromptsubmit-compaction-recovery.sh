#!/bin/bash
# UserPromptSubmit hook: PostCompact hook が残した marker を検出し､
# 直後のユーザープロンプトに additionalContext で復旧ガイドを注入する｡
#
# PostCompact は additionalContext を返せないため､この 2 段構成で
# 「圧縮直後の 1 ターンだけ復旧手順を Claude に伝える」を実現する｡
# marker は一度読んだら削除する (one-shot)｡
#
# fail-open: 個別操作失敗は log_error しつつ常に exit 0 で戻る｡

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="userpromptsubmit-compaction-recovery"

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

MARKER_FILE="$COMPACTED_DIR/$SESSION_ID"
[[ -f "$MARKER_FILE" ]] || exit 0

STATE_FILE=$(compact_state_file "$SESSION_ID")

# 復旧ガイドは 1 本にまとめ､state file の有無だけ変数で埋める｡
if [[ -f "$STATE_FILE" ]]; then
  STATE_STATUS="**あり**: \`$STATE_FILE\` を Read すると Active Plan / Current Phase / TaskList Summary / Session Decisions / Constraints and Blockers / Worker Topology / Editing Files / Recovery Notes の 8 セクションから現状を復元できます｡"
else
  STATE_STATUS="**なし**: 事前に \`/agents-daemon:compact-prep\` が呼ばれていません｡\`~/.claude/plans/\` から作業中の plan を探し､必要ならユーザーに現在のフェーズを確認してください｡次回は \`/compact\` 前に \`/agents-daemon:compact-prep\` を推奨します｡"
fi

GUIDE=$(cat <<EOF
[compact-recovery]
直前のターンで /compact が実行されました｡圧縮サマリーは大意しか残っていないため､
以下の順で作業状態を復旧してください｡

1. state file: $STATE_STATUS
2. TaskList ツールで in-progress タスクを再登録する｡
3. Recovery Notes (state file 内) または plan file の次アクションから作業を再開する｡

このガイドはこの 1 ターンでのみ表示されます｡
EOF
)

if ! OUTPUT=$(compact_emit_prompt_context "$GUIDE"); then
  log_error "jq failed to emit additionalContext for $SESSION_ID"
  # marker は消しておく (次のプロンプトで再試行しない)
  rm -f "$MARKER_FILE" || log_error "rm failed: $MARKER_FILE"
  exit 0
fi

printf '%s\n' "$OUTPUT"

# one-shot: marker を削除する｡
if ! rm -f "$MARKER_FILE"; then
  log_error "rm failed: $MARKER_FILE"
fi

exit 0
