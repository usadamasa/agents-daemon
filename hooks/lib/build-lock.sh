#!/bin/bash
# build-lock.sh
# build-daemon.sh と build-gui.sh が共有する、同時の build を 1 本に絞る lock。source して使う (実行はしない)。
# source 前に BUILD_LOCK (lock に使うディレクトリ) を設定し、hook-logger.sh を source しておく。
#
# lock は mkdir の原子性で取り、持ち主の PID を中に置く。取れなかったときは持ち主が生きているかで「ビルド中」と
# 「残骸」を分ける。残骸 (SIGKILL 等で cleanup が走らなかった) は消して取り直す。mkdir と pid の書き込みの間に
# 別の build が覗くと残骸と誤認するが、起きるのは二重 build 1 回で、成果物は mv で原子的に置かれるので壊れない。

take_lock() {
  mkdir "$BUILD_LOCK" 2>/dev/null || return 1
  if ! printf '%s\n' "$$" >"$BUILD_LOCK/pid"; then
    log_error "write failed: $BUILD_LOCK/pid"
    release_lock
    return 1
  fi
}

release_lock() {
  rm -f "$BUILD_LOCK/pid" || log_error "rm failed: $BUILD_LOCK/pid"
  rmdir "$BUILD_LOCK" || log_error "rmdir failed: $BUILD_LOCK"
}

# lock の持ち主の PID を stdout に出す。pid ファイルが無いか形が不正なら空。
lock_holder() {
  local pid=""
  [ -f "$BUILD_LOCK/pid" ] && pid=$(<"$BUILD_LOCK/pid")
  [[ "$pid" =~ ^[0-9]+$ ]] && printf '%s\n' "$pid"
}

lock_is_stale() {
  local pid
  pid=$(lock_holder)
  [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null
}

# lock を取る。別の build が持っていれば INFO を出して非 0 を返す (呼び手は exit 0 で抜ける)。
claim_lock() {
  take_lock && return 0
  if ! lock_is_stale; then
    log_info "another build (pid $(lock_holder)) holds $BUILD_LOCK; skipping"
    return 1
  fi
  log_info "stale lock $BUILD_LOCK (holder pid '$(lock_holder)' is gone); removing it"
  release_lock
  if ! take_lock; then
    log_info "another build took $BUILD_LOCK first; skipping"
    return 1
  fi
}
