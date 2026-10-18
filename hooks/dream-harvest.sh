#!/bin/bash
# Stop hook: `agents-daemon dream harvest --incremental --detach` を exec する。
#
# dream (記憶階層の consolidation) の Harvest phase を、全プロジェクト横断で Stop のたびに回す。
# detach・ログ (logs/dream-harvest.log)・失敗の時刻付けはバイナリ側 (cmd/agents-daemon/internal/dreamcli、internal/dream/harvest) が持つ。
# この hook が担うのはバイナリの場所の解決だけで、無ければ何もしない (SessionStart hook が build する)。
#
# この hook は transcript を読まない。読むのは起こしたバイナリで、Stop の時点で書き切れていないセッションを
# 次の harvest で読み直す前提は skills/dream/references/cli.md の「Stop hook 連携」にある。
#
# stdout は Claude Code が読むので何も出さない。

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="dream-harvest"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"

[ -x "$DAEMON_BIN" ] || exit 0

# バイナリの出力が context へ混ざらないよう stdout も stderr へ寄せる。
exec "$DAEMON_BIN" dream harvest --incremental --detach >&2
