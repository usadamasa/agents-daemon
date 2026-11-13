#!/bin/bash
# SessionStart hook: agents-daemon の daemon を起こす。
#
# plugin には install 時の lifecycle hook が無いため、バイナリの用意もここで担う。
# - cache のバイナリが plugin の version 以上から建てたものなら `daemon --ensure` を exec する (stamp_current)
# - stamp が無い・古い・読めなければ build-daemon.sh を detach して即座に戻る。build 完了後の起動は
#   build-daemon.sh が行う。SessionStart を go build で待たせない
# - daemon は最新で依存図のアプリ (agent-tasks) だけ古ければ、build-gui.sh を detach する
# - 前の build の失敗印 (build.failed、build-gui.failed) があれば、原因と建て直すコマンドを systemMessage に載せる。
#   build は detach するので、失敗はそのセッションではなく次の SessionStart で伝わる
#
# stdout は会話の context へ注入されるため、systemMessage の JSON のほかは何も出さない。ログは stderr と build.log へ送る。
# cc-premise: 2.1.323 hooks SessionStart の stdout の {"systemMessage": ...} を利用者に見せる

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

prepare_build_log() {
  local log_dir
  log_dir=$(dirname "$DAEMON_BUILD_LOG")
  if ! mkdir -p "$log_dir"; then
    log_error "mkdir failed: $log_dir"
    exit 1
  fi
}

# 失敗印は 1 行目が原因、2 行目が建て直すコマンド。行のまま載せる。
report_build_failures() {
  local -a lines=()
  if [ -f "$DAEMON_BUILD_FAILED" ]; then
    lines+=("agents-daemon: daemon のビルドに失敗しています。詳細は $DAEMON_BUILD_LOG を見てください。" "$(<"$DAEMON_BUILD_FAILED")")
  fi
  if [ -f "$GUI_BUILD_FAILED" ]; then
    lines+=("agents-daemon: 依存図のアプリ (agent-tasks) のビルドに失敗しています。詳細は $DAEMON_BUILD_LOG を見てください。" "$(<"$GUI_BUILD_FAILED")")
  fi
  if [ "${#lines[@]}" -eq 0 ]; then
    return 0
  fi
  if ! jq -n --arg msg "$(printf '%s\n' "${lines[@]}")" '{systemMessage: $msg}'; then
    log_error "cannot report build failures as systemMessage"
  fi
}

report_build_failures

if [ -x "$DAEMON_BIN" ] && stamp_current "$DAEMON_STAMP" "$version"; then
  # daemon は plugin 以上。依存図のアプリだけ古い (前回の build で toolchain が無かった、など) なら、それだけ建て直す。
  # macOS 以外では build-gui.sh が飛ばすだけなので起こさない。
  if [ "$(uname -s)" = Darwin ] && ! stamp_current "$GUI_STAMP" "$version"; then
    prepare_build_log
    log_info "agent-tasks missing or built from another version; building $version in background (log: $DAEMON_BUILD_LOG)"
    nohup "$SCRIPT_DIR/build-gui.sh" </dev/null >>"$DAEMON_BUILD_LOG" 2>&1 &
  fi
  # daemon の出力が context へ混ざらないよう stdout も stderr へ寄せる。
  exec "$DAEMON_BIN" daemon --ensure >&2
fi

prepare_build_log

log_info "binary missing or built from another version; building $version in background (log: $DAEMON_BUILD_LOG)"
# stdin も切らないと、呼び出し元が build の終了までパイプを待ち続ける。
nohup "$SCRIPT_DIR/build-daemon.sh" </dev/null >>"$DAEMON_BUILD_LOG" 2>&1 &

exit 0
