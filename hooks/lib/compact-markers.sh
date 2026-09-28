#!/bin/bash
# compact-markers.sh
# /compact 系 hook + compact-prep skill + agents-daemon の daemon で共有する
# marker directory 定数と helper｡source して使う (実行はしない)｡
#
# 使い方:
#   source "$SCRIPT_DIR/lib/compact-markers.sh"
#   session_id=$(compact_read_session_id)
#   compact_emit_prompt_context "guide text"

# shellcheck disable=SC2034
# 以下の定数は source した外部スクリプトから参照される｡この file 内では未使用に見えるが正常｡

# base は agents-daemon の XDG state ディレクトリ｡daemon 側の internal/apppath と同じ規則で､
# rate-limits/ と兄弟になる｡片方だけ変えると protocol が黙って壊れる｡
#
# cwd 相対にはしない｡producer (statusline / hook / skill) と consumer (daemon) が
# 別プロセスであり､hook に渡る cwd は Claude が cd したり worktree に入ると動くため､
# 書く場所と読む場所が食い違って protocol が黙って壊れる｡
# $TMPDIR も使えない (sandboxed な skill と excluded で起動する hook とで解決値が違う)｡
_CM_BASE="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"

# PostCompact hook が書く｡直後の UserPromptSubmit recovery hook が復旧ガイド注入のトリガーに使う (one-shot)｡
COMPACTED_DIR="$_CM_BASE/compacted"

# compact-prep skill が書く state file の置き場｡ファイル名は "<session_id>.md"｡
# daemon はこのファイルの mtime を「compact-prep が走った」の判定に使う｡
COMPACT_STATE_DIR="$_CM_BASE/compact-state"

# session_id ごとの state file パスを返す｡
compact_state_file() {
  printf '%s\n' "$COMPACT_STATE_DIR/$1.md"
}

# stdin JSON から .session_id を stdout に出す (空なら empty string)｡
# 呼び出し側はこの 1 回で SESSION_ID を得られる｡フォークは jq 1 回のみ｡
compact_read_session_id() {
  jq -r '.session_id // empty' 2>/dev/null
}

# UserPromptSubmit hook 向け additionalContext JSON を stdout に出す｡
# 第 1 引数に本文文字列｡jq が JSON エスケープを担う｡
compact_emit_prompt_context() {
  jq -cn --arg ctx "$1" \
    '{hookSpecificOutput: {hookEventName: "UserPromptSubmit", additionalContext: $ctx}}'
}
