#!/bin/bash
# plugin のソースから agents-daemon を go build し､XDG cache の固定パスへ置いて起動する｡
# ensure-daemon.sh (SessionStart hook) が detach して呼ぶ｡出力は build.log へ流れる｡
#
# - 同時に起きた build は mkdir の lock で 1 本に絞る｡持ち主が生きていれば「ビルド中」として終わり､
#   死んでいれば残骸として消して取り直す
# - 建てたバイナリは同一ディレクトリの一時ファイルから mv で差し替える｡動作中の daemon は
#   実行ファイルの差し替えを検知して自分で入れ替わるので､ここでは止めない
# - go が無ければエラー終了する｡PATH に何が居るかは仮定しない (入れ方は README)

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK_NAME="build-daemon"

if [ ! -f "$SCRIPT_DIR/lib/hook-logger.sh" ]; then
  printf 'ERROR: [%s] hook-logger.sh not found at %s\n' "$HOOK_NAME" "$SCRIPT_DIR/lib/hook-logger.sh" >&2
  exit 1
fi
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/hook-logger.sh"
# shellcheck disable=SC1091 # Dynamically resolved path
source "$SCRIPT_DIR/lib/daemon-bin.sh"

if ! mkdir -p "$DAEMON_BIN_DIR"; then
  log_error "mkdir failed: $DAEMON_BIN_DIR"
  exit 1
fi

# lock は mkdir の原子性で取り､持ち主の PID を中に置く｡取れなかったときは持ち主が生きているかで
# 「ビルド中」と「残骸」を分ける｡残骸 (SIGKILL 等で cleanup が走らなかった) は消して取り直す｡
# mkdir と pid の書き込みの間に別の build が覗くと残骸と誤認するが､起きるのは二重 build 1 回で､
# バイナリは mv で原子的に置かれるので壊れない｡
take_lock() {
  mkdir "$DAEMON_BUILD_LOCK" 2>/dev/null || return 1
  if ! printf '%s\n' "$$" >"$DAEMON_BUILD_LOCK/pid"; then
    log_error "write failed: $DAEMON_BUILD_LOCK/pid"
    release_lock
    return 1
  fi
}

release_lock() {
  rm -f "$DAEMON_BUILD_LOCK/pid" || log_error "rm failed: $DAEMON_BUILD_LOCK/pid"
  rmdir "$DAEMON_BUILD_LOCK" || log_error "rmdir failed: $DAEMON_BUILD_LOCK"
}

# lock の持ち主の PID を stdout に出す｡pid ファイルが無いか形が不正なら空｡
lock_holder() {
  local pid=""
  [ -f "$DAEMON_BUILD_LOCK/pid" ] && pid=$(<"$DAEMON_BUILD_LOCK/pid")
  [[ "$pid" =~ ^[0-9]+$ ]] && printf '%s\n' "$pid"
}

lock_is_stale() {
  local pid
  pid=$(lock_holder)
  [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null
}

if ! take_lock; then
  if ! lock_is_stale; then
    log_info "another build (pid $(lock_holder)) holds $DAEMON_BUILD_LOCK; skipping"
    exit 0
  fi
  log_info "stale lock $DAEMON_BUILD_LOCK (holder pid '$(lock_holder)' is gone); removing it"
  release_lock
  if ! take_lock; then
    log_info "another build took $DAEMON_BUILD_LOCK first; skipping"
    exit 0
  fi
fi

tmp_bin=""
tmp_stamp=""
cleanup() {
  if [ -n "$tmp_bin" ] && [ -e "$tmp_bin" ]; then
    rm -f "$tmp_bin" || log_error "rm failed: $tmp_bin"
  fi
  if [ -n "$tmp_stamp" ] && [ -e "$tmp_stamp" ]; then
    rm -f "$tmp_stamp" || log_error "rm failed: $tmp_stamp"
  fi
  release_lock
}
# lock を取った後で trap を張る｡先に張ると､lock を取れずに抜ける経路が他の build の lock を消す｡
trap cleanup EXIT
trap 'exit 1' INT TERM

if ! command -v go >/dev/null 2>&1; then
  log_error "go not found in PATH; install the Go toolchain to build agents-daemon (see README)"
  exit 1
fi

if ! version=$(plugin_version); then
  log_error "cannot read version from $PLUGIN_ROOT/.claude-plugin/plugin.json"
  exit 1
fi

log_info "$(date '+%Y-%m-%dT%H:%M:%S%z') building agents-daemon $version from $PLUGIN_ROOT"

if ! tmp_bin=$(mktemp "$DAEMON_BIN_DIR/.agents-daemon.XXXXXX"); then
  log_error "mktemp failed in $DAEMON_BIN_DIR"
  exit 1
fi
if ! go build -C "$PLUGIN_ROOT" -o "$tmp_bin" ./cmd/agents-daemon; then
  log_error "go build failed"
  exit 1
fi
if ! mv "$tmp_bin" "$DAEMON_BIN"; then
  log_error "mv failed: $tmp_bin -> $DAEMON_BIN"
  exit 1
fi
tmp_bin=""

# stamp はバイナリを置いた後に書く｡逆順だと､途中で落ちたとき古いバイナリが新しい version を名乗る｡
if ! tmp_stamp=$(mktemp "$DAEMON_BIN_DIR/.agents-daemon.version.XXXXXX"); then
  log_error "mktemp failed in $DAEMON_BIN_DIR"
  exit 1
fi
if ! printf '%s\n' "$version" >"$tmp_stamp"; then
  log_error "write failed: $tmp_stamp"
  exit 1
fi
if ! mv "$tmp_stamp" "$DAEMON_STAMP"; then
  log_error "mv failed: $tmp_stamp -> $DAEMON_STAMP"
  exit 1
fi
tmp_stamp=""

log_info "built $DAEMON_BIN ($version)"

if ! "$DAEMON_BIN" daemon --ensure; then
  log_error "$DAEMON_BIN daemon --ensure failed"
  exit 1
fi
