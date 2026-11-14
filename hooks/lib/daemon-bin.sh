#!/bin/bash
# daemon-bin.sh
# hook と build の script が共有する、daemon バイナリの置き場、plugin version の解決、stamp との比較。
# source して使う (実行はしない)。source 前に SCRIPT_DIR (hooks/) を設定しておく。

# shellcheck disable=SC2034
# 以下の定数は source した外部スクリプトから参照される。

# plugin の root。hooks.json は ${CLAUDE_PLUGIN_ROOT} で hook を呼ぶが、hook 自身は
# 環境変数に頼らず自分の位置から解決する (bats から直接呼んでも同じ値になる)。
PLUGIN_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

# plugin の install 先はバージョンごとに変わるため、バイナリはその外の XDG cache に置く。
# 動作中の daemon は自分の実行ファイルの差し替えを検知して入れ替わるので、置き場は固定にする。
DAEMON_BIN_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin"
DAEMON_BIN="$DAEMON_BIN_DIR/agents-daemon"
# どの plugin version のソースから建てたかを記録する。plugin を更新したら建て直す合図になる。
DAEMON_STAMP="$DAEMON_BIN_DIR/agents-daemon.version"
# 同時に起きた SessionStart が二重に build しないための lock (mkdir の原子性を使う)。
DAEMON_BUILD_LOCK="$DAEMON_BIN_DIR/.build.lock"
DAEMON_BUILD_LOG="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/logs/build.log"
# go build の失敗印。1 行目が原因、2 行目が建て直すコマンド。次の SessionStart が systemMessage に載せ、
# 建てられたら build の script が消す (go-build.sh)。
DAEMON_BUILD_FAILED="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/build.failed"
GUI_BUILD_FAILED="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/build-gui.failed"

# 依存図のアプリ (gui/、agent-tasks)。バイナリは daemon と同じ cache に置き、起動用の .app は ~/Applications に組む。
# stamp と lock は daemon と別 (macOS 以外や toolchain の無い環境では gui だけ建たない)。
GUI_BIN="$DAEMON_BIN_DIR/agent-tasks"
GUI_STAMP="$DAEMON_BIN_DIR/agent-tasks.version"
GUI_BUILD_LOCK="$DAEMON_BIN_DIR/.build-gui.lock"
GUI_APP="$HOME/Applications/agent-tasks.app"

# plugin.json の version を stdout に出す。読めなければ非 0。
plugin_version() {
  jq -er '.version' "$PLUGIN_ROOT/.claude-plugin/plugin.json"
}

# version_older A B: A が B より古ければ 0、同じか新しければ 1、どちらかが `.` 区切りの数でなければ 2。
# 欄ごとに 10 進の数として比べる (`0926` を 8 進と読まず、`9` と `10` を文字列順で比べない)。
version_older() {
  local -a a b
  local i x y n
  [ -n "$1" ] && [ -n "$2" ] || return 2
  IFS=. read -ra a <<<"$1"
  IFS=. read -ra b <<<"$2"
  n=${#a[@]}
  [ "${#b[@]}" -gt "$n" ] && n=${#b[@]}
  [ "$n" -gt 0 ] || return 2
  for ((i = 0; i < n; i++)); do
    # 欄の数が違うときだけ足りない欄を 0 にする。空の欄 (`2026..0`) は 0 にせず、数でないとして弾く。
    x=${a[i]-0}
    y=${b[i]-0}
    [[ "$x" =~ ^[0-9]+$ && "$y" =~ ^[0-9]+$ ]] || return 2
    if ((10#$x < 10#$y)); then
      return 0
    fi
    if ((10#$x > 10#$y)); then
      return 1
    fi
  done
  return 1
}

# stamp_current FILE VERSION: stamp file があり、その version が VERSION 以上なら 0。無い・古い・読めないなら 1。
# 建て直すかの判断に使う。新しい stamp を古い plugin のソースで上書きしない (update 後に旧 plugin のセッションが
# compact や clear で SessionStart を起こしても、バイナリを古いものへ戻さない)。
stamp_current() {
  local rc=0
  [ -f "$1" ] || return 1
  version_older "$(<"$1")" "$2" || rc=$?
  [ "$rc" -eq 1 ]
}

# daemon_bin_serves STAMP VERSION: cache のバイナリ (STAMP の version から建った) に plugin (VERSION) の hook の
# サブコマンドを渡してよければ 0。stamp が plugin より古いか読めなければ、理由を stderr に 1 行出して 1。
# 古いバイナリはサブコマンドを知らず `unknown command` で exit 1 になりうる。新しいバイナリは知っているので通す
# (update の後も、旧 plugin のまま起動中のセッションの記録を止めない)。log_skip を使うので hook-logger.sh も source する。
# 新しいバイナリが古い hook の呼び出しを受けられることが前提なので、hook から呼ぶサブコマンドと引数は消さず、
# 変えずに足すだけにする。
daemon_bin_serves() {
  local stamp="$1" version="$2" rc=0
  version_older "$stamp" "$version" || rc=$?
  case "$rc" in
    1) return 0 ;;
    0) log_skip "binary $stamp is older than plugin $version; waiting for ensure-daemon.sh to rebuild" ;;
    *) log_skip "cannot compare binary stamp '$stamp' with plugin $version" ;;
  esac
  return 1
}
