#!/bin/bash
# SessionStart hook: agents-daemon の daemon を起こす｡
#
# plugin には install 時の lifecycle hook が無いため､バイナリの用意もここで担う｡
# - cache のバイナリが plugin の version から建てたものなら `daemon --ensure` を exec する
# - そうでなければ build-daemon.sh を detach して即座に戻る｡build 完了後の起動は
#   build-daemon.sh が行う｡SessionStart を go build で待たせない
#
# stdout は会話の context へ注入されるため､何も出さない｡ログは stderr と build.log へ送る｡

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="ensure-daemon"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"

if ! version=$(plugin_version); then
  log_error "cannot read version from $PLUGIN_ROOT/.claude-plugin/plugin.json"
  exit 1
fi

if [ -x "$DAEMON_BIN" ] && [ -f "$DAEMON_STAMP" ] && [ "$(<"$DAEMON_STAMP")" = "$version" ]; then
  # daemon の出力が context へ混ざらないよう stdout も stderr へ寄せる｡
  exec "$DAEMON_BIN" daemon --ensure >&2
fi

log_dir=$(dirname "$DAEMON_BUILD_LOG")
if ! mkdir -p "$log_dir"; then
  log_error "mkdir failed: $log_dir"
  exit 1
fi

log_info "binary missing or built from another version; building $version in background (log: $DAEMON_BUILD_LOG)"
# stdin も切らないと､呼び出し元が build の終了までパイプを待ち続ける｡
nohup "$SCRIPT_DIR/build-daemon.sh" </dev/null >>"$DAEMON_BUILD_LOG" 2>&1 &

exit 0
