#!/bin/bash
# SessionStart hook: `agents-daemon prune-worktrees --hook` に stdin をそのまま渡す。
#
# 誰も使っていない Claude Code の worktree (`.claude/worktrees/` 配下) を、今開いたリポジトリの分だけ回収する。
# 判定・git 操作・hook 入力 (cwd) の読み取りはバイナリ側 (internal/worktree、cmd_prune_worktrees.go) が持つ。
# この hook が担うのはバイナリの場所の解決だけで、無ければ何もしない (ensure-daemon.sh が build する)。
#
# stdout は Claude Code が読む。バイナリは回収したときだけ {"systemMessage": ...} を出し、それ以外は何も出さない。
# この hook はその stdout をそのまま通す (ログは stderr へ)。

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="prune-worktrees"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"

[ -x "$DAEMON_BIN" ] || exit 0

exec "$DAEMON_BIN" prune-worktrees --hook
