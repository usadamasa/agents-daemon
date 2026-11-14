#!/bin/bash
# SessionStart hook: agents-daemon の daemon を起こす。
#
# plugin には install 時の lifecycle hook が無いため、バイナリの用意もここで担う。
# - cache のバイナリが plugin の version 以上から建てたものなら `daemon --ensure` を exec する (stamp_current)
# - stamp が無い・古い・読めなければ build-daemon.sh を detach して即座に戻る。build 完了後の起動は
#   build-daemon.sh が行う。SessionStart を go build で待たせない
# - daemon は最新で依存図のアプリ (agent-tasks) だけ古ければ、build-gui.sh を detach する
# - これから建て直す成果物に前の build の失敗印 (build.failed、build-gui.failed) があれば、原因と建て直すコマンドを
#   systemMessage に載せる。build は detach するので、失敗はそのセッションではなく次の SessionStart で伝わる。
#   stamp が current なら建て直さないので載せない (task install や worktree の build が残した失敗印で、動いているものに
#   「失敗しています」と出さない)
#
# stdout は会話の context へ注入されるため、systemMessage の JSON のほかは何も出さない。ログは stderr と build.log へ送る。
# cc-premise: 2.1.323 hooks SessionStart の stdout の {"systemMessage": ...} を利用者に見せる (stdout の JSON までを確かめ、表示は未観測)

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

# report_build_failures MARKER...: 失敗印のあるものを 1 つの systemMessage にまとめて stdout へ出す。
# build を detach する前に呼ぶ (後だと、走り出した build が印を消す・書き直すのと競合する)。
# 載せるのは印の 1 行目 (原因) だけ。建て直すコマンドはこの hook の置き場から組む。印の 2 行目は失敗した時点の
# plugin の置き場を指し、plugin を更新した後は消えているかもしれない。
report_build_failures() {
  local -a lines=()
  local marker cause what script
  for marker in "$@"; do
    if [ ! -f "$marker" ]; then
      continue
    fi
    # 判定と読み取りの間に、走っている build が成功して消すことがある
    if ! IFS= read -r cause <"$marker"; then
      log_error "read failed: $marker"
      continue
    fi
    if [ "$marker" = "$GUI_BUILD_FAILED" ]; then
      what="依存図のアプリ (agent-tasks)"
      script="$SCRIPT_DIR/build-gui.sh"
    else
      what="daemon"
      script="$SCRIPT_DIR/build-daemon.sh"
    fi
    lines+=("agents-daemon: $what のビルドに失敗しています。詳細は $DAEMON_BUILD_LOG を見てください。" "$cause"
      "セッションを開き直さずに建て直すには、Claude Code で ! $script を打つ")
  done
  if [ "${#lines[@]}" -eq 0 ]; then
    return 0
  fi
  if ! jq -n --arg msg "$(printf '%s\n' "${lines[@]}")" '{systemMessage: $msg}'; then
    log_error "cannot report build failures as systemMessage"
  fi
}

if [ -x "$DAEMON_BIN" ] && stamp_current "$DAEMON_STAMP" "$version"; then
  # daemon は plugin 以上。依存図のアプリだけ古い (前回の build で toolchain が無かった、など) なら、それだけ建て直す。
  # macOS 以外では build-gui.sh が飛ばすだけなので起こさない。
  if [ "$(uname -s)" = Darwin ] && ! stamp_current "$GUI_STAMP" "$version"; then
    report_build_failures "$GUI_BUILD_FAILED"
    prepare_build_log
    log_info "agent-tasks missing or built from another version; building $version in background (log: $DAEMON_BUILD_LOG)"
    nohup "$SCRIPT_DIR/build-gui.sh" </dev/null >>"$DAEMON_BUILD_LOG" 2>&1 &
  fi
  # daemon の出力が context へ混ざらないよう stdout も stderr へ寄せる。
  exec "$DAEMON_BIN" daemon --ensure >&2
fi

# build-daemon.sh は続けて agent-tasks も建て直すので、両方の失敗印を載せる。
report_build_failures "$DAEMON_BUILD_FAILED" "$GUI_BUILD_FAILED"
prepare_build_log

log_info "binary missing or built from another version; building $version in background (log: $DAEMON_BUILD_LOG)"
# stdin も切らないと、呼び出し元が build の終了までパイプを待ち続ける。
nohup "$SCRIPT_DIR/build-daemon.sh" </dev/null >>"$DAEMON_BUILD_LOG" 2>&1 &

exit 0
