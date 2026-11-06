#!/bin/bash
# daemon-bin.sh
# ensure-daemon.sh と build-daemon.sh が共有する、daemon バイナリの置き場と plugin version の解決。
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
