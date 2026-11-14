#!/usr/bin/env bats
# SessionStart hook (ensure-daemon.sh) の分岐を検証する。
#
# - cache のバイナリがあり、stamp が plugin の version 以上なら daemon --ensure を exec する (新しい stamp を
#   古い plugin のソースで上書きしない)
# - stamp が無い・古い・読めなければ build-daemon.sh を detach して即座に戻る (go build を前景で回さない)
# - go が無ければ build.log に ERROR を残し、バイナリを作らない
# - 建て直す成果物に前の build の失敗印があれば、原因と建て直すコマンドを systemMessage に載せる
#
# SessionStart hook の stdout は会話の context へ注入されるため、失敗印が無ければ stdout を空に保つ。

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/ensure-daemon.sh"
}

teardown() {
  teardown_daemon_fixture
}

@test "バイナリと stamp が揃っていれば daemon --ensure を exec し、build しない" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '%s\n' "$PLUGIN_VERSION" >"$STAMP"
  printf '%s\n' "$PLUGIN_VERSION" >"$GUI_STAMP"
  install_fake_go

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ ! -e "$BUILD_LOG" ]
}

@test "stamp が plugin の version より新しければ建て直さず daemon --ensure を exec する (旧 plugin のセッションが古いソースで上書きしない)" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '9999.9999.9\n' >"$STAMP"
  printf '9999.9999.9\n' >"$GUI_STAMP"
  install_fake_go
  install_fake_gui_toolchain

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ ! -e "$BUILD_LOG" ]
  [ "$(<"$STAMP")" = "9999.9999.9" ]
  [ "$(<"$GUI_STAMP")" = "9999.9999.9" ]
}

@test "stamp が読めなければ建て直す" {
  make_fake_daemon "$TEST_TMPDIR/old-daemon.calls" "$BIN"
  printf 'garbage\n' >"$STAMP"
  install_fake_go

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  wait_until stamp_is_current
  wait_until daemon_ensured
}

@test "daemon は最新で agent-tasks の stamp だけ違えば、gui の build を detach してから daemon --ensure を exec する" {
  require_darwin
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '%s\n' "$PLUGIN_VERSION" >"$STAMP"
  printf '%s\n' "0000.0000.0" >"$GUI_STAMP"
  install_fake_go
  install_fake_gui_toolchain

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]

  wait_until gui_stamp_is_current
  [ -x "$GUI_APP/Contents/MacOS/agent-tasks" ]
  # daemon は建て直さない
  ! grep -q -- " ./cmd/agents-daemon" "$TEST_TMPDIR/go.calls"
  [ "$(<"$STAMP")" = "$PLUGIN_VERSION" ]
}

@test "stamp が plugin の version より古ければ build を detach し、完了後に新しいバイナリで daemon --ensure する" {
  make_fake_daemon "$TEST_TMPDIR/old-daemon.calls" "$BIN"
  printf '%s\n' "0000.0000.0" >"$STAMP"
  install_fake_go
  # fake go は release が置かれるまで終わらない。hook がそれを待たずに戻ることを確かめる。
  export GO_STUB_WAIT_FOR="$TEST_TMPDIR/release"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  # hook が戻った時点では build は終わっていない
  [ "$(<"$STAMP")" = "0000.0000.0" ]

  touch "$TEST_TMPDIR/release"
  wait_until stamp_is_current
  wait_until daemon_ensured

  # 古いバイナリは起動されず、go build が plugin の root で cmd/agents-daemon を建てた
  [ ! -e "$TEST_TMPDIR/old-daemon.calls" ]
  grep -q -- "build -C $REPO_ROOT " "$TEST_TMPDIR/go.calls"
  grep -q -- " -o " "$TEST_TMPDIR/go.calls"
  grep -q -- " ./cmd/agents-daemon" "$TEST_TMPDIR/go.calls"
  # バイナリが `agents-daemon version` で名乗れるよう、plugin の version を埋め込む
  grep -q -- " -ldflags -X main.version=$(jq -r .version "$REPO_ROOT/.claude-plugin/plugin.json") " "$TEST_TMPDIR/go.calls"
  # build 用の一時ファイルと lock は残らない
  wait_until test ! -d "$LOCK"
  [ -z "$(find "$BIN_DIR" -name '.agents-daemon.*')" ]
}

@test "バイナリが無ければ build を detach して建てる" {
  install_fake_go

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]

  wait_until stamp_is_current
  wait_until daemon_ensured
  [ -x "$BIN" ]
}

# build は detach するので、失敗はそのセッションではなく次の SessionStart の systemMessage で伝える。
@test "daemon の失敗印があれば、建て直しを detach しつつ原因と建て直すコマンドを systemMessage に載せる" {
  install_fake_go
  mkdir -p "$(dirname "$BUILD_FAILED")"
  printf '%s\n' 'go build failed: toolchain not available' '! /x/hooks/build-daemon.sh' >"$BUILD_FAILED"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  msg=$(printf '%s' "$output" | jq -er '.systemMessage')
  [[ "$msg" == *"toolchain not available"* ]]
  # 建て直すコマンドは印の 2 行目 (失敗した時点の、更新で消えうる置き場) ではなく、今の plugin の置き場から組む
  [[ "$msg" == *"! $REPO_ROOT/hooks/build-daemon.sh"* ]]
  [[ "$msg" != *"/x/hooks"* ]]
  [[ "$msg" == *"$BUILD_LOG"* ]]
  wait_until stamp_is_current
}

@test "agent-tasks を建て直すとき失敗印があれば、daemon を起こしつつ原因と建て直すコマンドを systemMessage に載せる" {
  require_darwin
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '%s\n' "$PLUGIN_VERSION" >"$STAMP"
  printf '%s\n' "0000.0000.0" >"$GUI_STAMP"
  install_fake_go
  install_fake_gui_toolchain
  # 載せた後に detach した build が成功して失敗印を消さないよう、go を落とす
  export GO_STUB_EXIT=1
  mkdir -p "$(dirname "$GUI_BUILD_FAILED")"
  printf '%s\n' 'go build failed: cgo error' '! /x/hooks/build-gui.sh' >"$GUI_BUILD_FAILED"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  msg=$(printf '%s' "$output" | jq -er '.systemMessage')
  [[ "$msg" == *"agent-tasks"* ]]
  [[ "$msg" == *"cgo error"* ]]
  [[ "$msg" == *"! $REPO_ROOT/hooks/build-gui.sh"* ]]
  [[ "$msg" != *"/x/hooks"* ]]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
}

# 建て直さないなら失敗印は古い (task install や worktree の build が残したもの)。動いているものに「失敗しています」と出さない。
@test "stamp が両方 current なら、失敗印が残っていても stdout は空" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '%s\n' "$PLUGIN_VERSION" >"$STAMP"
  printf '%s\n' "$PLUGIN_VERSION" >"$GUI_STAMP"
  mkdir -p "$(dirname "$BUILD_FAILED")"
  printf '%s\n' 'go build failed: old' '! /x/hooks/build-daemon.sh' >"$BUILD_FAILED"
  printf '%s\n' 'go build failed: old' '! /x/hooks/build-gui.sh' >"$GUI_BUILD_FAILED"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
}

@test "go が PATH に無ければ build.log に ERROR を残し、バイナリを作らない" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]

  wait_until build_failed
  grep -q 'ERROR.*go not found in PATH' "$BUILD_LOG"
  [ ! -e "$BIN" ]
  [ ! -e "$STAMP" ]
}
