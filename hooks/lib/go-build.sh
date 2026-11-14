#!/bin/bash
# go-build.sh
# build-daemon.sh と build-gui.sh が共有する、go build の呼び出しと失敗印の読み書き。
# source して使う (実行はしない)。source 前に hook-logger.sh を source しておく。

# go_build OUT ARGS...: `go build ARGS` を走らせ、出力を stderr へ流しつつ OUT へ写す。終了コードは go のもの (pipefail)。
# GOTOOLCHAIN=auto はこの呼び出しにだけ渡す。セッションを開いたリポジトリの devShell が付けた GOTOOLCHAIN=local は
# そのリポジトリの方針なので受け継がない (受け継ぐと、PATH の go が go.mod より古いとき toolchain を取りに行けずに落ちる)。
# hook は sandbox の外で動くので取得できる。出力を捕まえずに流すのは、`!` で建て直すとき取得中の表示が見えるように。
go_build() {
  local -r out="$1"
  shift
  GOTOOLCHAIN=auto go build "$@" 2>&1 | tee "$out" >&2
}

# record_build_failure MARKER OUT SCRIPT: go の最後の行 (OUT の末尾) を原因の 1 行目、SCRIPT を `!` で打つ建て直しの
# コマンドを 2 行目にして MARKER へ書く。次の SessionStart (ensure-daemon.sh) が systemMessage に載せる。
# 失敗したセッションでは systemMessage がまだ出ないので、log にも同じ 2 行を残す。
# 置き場は Claude Code の sandbox から書けないので、建て直しは利用者が `!` で打つ。
record_build_failure() {
  local -r marker="$1" out="$2" script="$3"
  local last cause
  if ! last=$(tail -n 1 "$out"); then
    log_error "read failed: $out"
    last=""
  fi
  cause="go build failed"
  if [ -n "$last" ]; then
    cause="$cause: $last"
  fi
  local -r rebuild="セッションを開き直さずに建て直すには、Claude Code で ! $script を打つ"
  log_error "$cause"
  log_error "$rebuild"
  if ! mkdir -p "$(dirname "$marker")"; then
    log_error "mkdir failed: $(dirname "$marker")"
    return 0
  fi
  if ! printf '%s\n' "$cause" "$rebuild" >"$marker"; then
    log_error "write failed: $marker"
  fi
}

# clear_build_failure MARKER: 建てられたら前の失敗印を消す。
clear_build_failure() {
  local -r marker="$1"
  if [ -e "$marker" ] && ! rm -f "$marker"; then
    log_error "rm failed: $marker"
  fi
}
