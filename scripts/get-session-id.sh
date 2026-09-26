#!/bin/bash
set -euo pipefail
# get-session-id.sh
# 現セッションの session_id (UUID) を stdout に出力する｡compact-prep skill から呼ばれる｡
#
# skill は hook と違って stdin JSON を受け取らないため、Claude Code が Bash ツールの
# 環境に入れる CLAUDE_CODE_SESSION_ID を唯一の情報源にする｡この値は herdr の
# agent_session.value と一致するため、agents-daemon の daemon が pane から引く
# session_id と突き合わせられる｡
#
# ~/.claude/projects/<encoded-cwd>/*.jsonl の mtime から推測する形は取らない｡同一
# プロジェクトで複数セッションが動くと取り違え、他セッションの state file を上書きする｡
# 推測はせず、環境変数が無ければエラー終了する｡

if [ -z "${CLAUDE_CODE_SESSION_ID:-}" ]; then
  echo "get-session-id: CLAUDE_CODE_SESSION_ID not set" >&2
  exit 1
fi

# 呼び出し側はこの値をファイル名に使うため、パス区切りを含む値を通さない｡
if [[ ! "$CLAUDE_CODE_SESSION_ID" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "get-session-id: CLAUDE_CODE_SESSION_ID has unexpected form" >&2
  exit 1
fi

printf '%s\n' "$CLAUDE_CODE_SESSION_ID"
