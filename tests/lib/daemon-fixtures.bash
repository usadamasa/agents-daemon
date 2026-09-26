# daemon-fixtures.bash
# ensure-daemon.sh / build-daemon.sh のテスト用フィクスチャ｡
#
# PATH は差し引きではなく組み立てる｡hook が呼ぶコマンドだけを symlink した
# ディレクトリを PATH にすれば､開発機でも CI でも `go` が無い状態を同じ形で作れる｡

# hook・build・fake go が呼ぶ外部コマンド (bash の builtin は含めない)｡
DAEMON_FIXTURE_COMMANDS=(jq mkdir mv mktemp nohup dirname rm rmdir chmod sleep date)

# HOME と XDG の各ディレクトリを一時ディレクトリへ向け､PATH 用のディレクトリを作る｡
# setup から呼ぶ｡呼んだ後は FIXTURE_PATH・BIN_DIR・BIN・STAMP・BUILD_LOG・LOCK が使える｡
# shellcheck disable=SC2034 # 設定する変数は load した .bats 側が参照する
setup_daemon_fixture() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  TEST_TMPDIR=$(mktemp -d "${TMPDIR:-/tmp}/agents-daemon-test.XXXXXX")
  export TEST_TMPDIR

  export HOME="$TEST_TMPDIR/home"
  export XDG_CACHE_HOME="$TEST_TMPDIR/cache"
  export XDG_STATE_HOME="$TEST_TMPDIR/state"
  mkdir -p "$HOME"

  BIN_DIR="$XDG_CACHE_HOME/agents-daemon/bin"
  BIN="$BIN_DIR/agents-daemon"
  STAMP="$BIN_DIR/agents-daemon.version"
  LOCK="$BIN_DIR/.build.lock"
  BUILD_LOG="$XDG_STATE_HOME/agents-daemon/logs/build.log"
  PLUGIN_VERSION=$(jq -er '.version' "$REPO_ROOT/.claude-plugin/plugin.json")

  FIXTURE_PATH="$TEST_TMPDIR/pathbin"
  mkdir -p "$FIXTURE_PATH"
  local c src
  for c in "${DAEMON_FIXTURE_COMMANDS[@]}"; do
    src=$(command -v "$c")
    ln -s "$src" "$FIXTURE_PATH/$c"
  done
}

teardown_daemon_fixture() {
  if [ -n "${TEST_TMPDIR:-}" ]; then
    rm -rf "$TEST_TMPDIR"
  fi
}

# 呼ばれた引数を $1 のファイルへ追記するだけの daemon バイナリを $2 に置く｡
make_fake_daemon() {
  local record="$1" dest="$2"
  mkdir -p "$(dirname "$dest")"
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >>"%s"\n' "$record" >"$dest"
  chmod +x "$dest"
}

# FIXTURE_PATH に fake の go (tests/fixtures/fake-go) を置く｡
install_fake_go() {
  cp "$REPO_ROOT/tests/fixtures/fake-go" "$FIXTURE_PATH/go"
  chmod +x "$FIXTURE_PATH/go"
}

# 条件コマンドが成功するまで最大 10 秒待つ｡detach した build の完了待ちに使う｡
wait_until() {
  local i
  for ((i = 0; i < 200; i++)); do
    if "$@"; then
      return 0
    fi
    sleep 0.05
  done
  return 1
}

stamp_is_current() {
  [ -f "$STAMP" ] && [ "$(<"$STAMP")" = "$PLUGIN_VERSION" ]
}

daemon_ensured() {
  [ -f "$TEST_TMPDIR/daemon.calls" ] && grep -qx 'daemon --ensure' "$TEST_TMPDIR/daemon.calls"
}

build_failed() {
  [ ! -d "$LOCK" ] && [ -f "$BUILD_LOG" ] && grep -q 'ERROR' "$BUILD_LOG"
}
