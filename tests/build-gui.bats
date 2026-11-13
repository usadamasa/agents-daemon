#!/usr/bin/env bats
# build-gui.sh (依存図のアプリ agent-tasks を建てて ~/Applications に置く) を検証する。
#
# - macOS 以外か、go / cc / codesign が無ければ INFO 1 行で飛ばし、何も書かない (stamp も書かないので次の SessionStart が
#   また試す)
# - 建てたら cache のバイナリ、.app の bundle、stamp がそろい、バイナリと Info.plist に plugin の version が入る
# - bundle の Resources にアイコン (gui/darwin/agent-tasks.icns) が入り、Info.plist の CFBundleIconFile が指す
# - go build か codesign が失敗したら、前の bundle と stamp を残し、一時ファイルと lock を片付ける
# - 同時の build は lock で 1 本に絞る (build-daemon.sh と同じ)

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  BUILD="$REPO_ROOT/hooks/build-gui.sh"
}

teardown() {
  teardown_daemon_fixture
}

@test "cc か codesign が無ければ INFO で飛ばし、何も書かない" {
  install_fake_go

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"skipping"* ]]
  [[ "$output" != *"ERROR"* ]]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ ! -e "$GUI_BIN" ]
  [ ! -e "$GUI_STAMP" ]
  [ ! -d "$GUI_LOCK" ]
}

@test "go が無ければ INFO で飛ばす (daemon の build と違って ERROR にしない)" {
  install_fake_gui_toolchain

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"skipping"* ]]
  [[ "$output" != *"ERROR"* ]]
  [ ! -e "$GUI_STAMP" ]
}

@test "建てると cache のバイナリと .app の bundle と stamp がそろう" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ -x "$GUI_BIN" ]
  [ -x "$GUI_APP/Contents/MacOS/agent-tasks" ]
  [ "$(<"$GUI_STAMP")" = "$PLUGIN_VERSION" ]
  # go build は gui/ の module で、-C で入って . を建てる
  grep -q -- "build -C $REPO_ROOT/gui " "$TEST_TMPDIR/go.calls"
  grep -q -- " -o " "$TEST_TMPDIR/go.calls"
  grep -q -- " \.$" "$TEST_TMPDIR/go.calls"
  # plugin の version をバイナリ (窓の title) と Info.plist (About の panel) に入れる
  grep -q -- " -ldflags -X main.version=$PLUGIN_VERSION " "$TEST_TMPDIR/go.calls"
  [ "$(plutil -extract CFBundleShortVersionString raw "$GUI_APP/Contents/Info.plist")" = "$PLUGIN_VERSION" ]
  [ "$(plutil -extract CFBundleVersion raw "$GUI_APP/Contents/Info.plist")" = "$PLUGIN_VERSION" ]
  [ "$(plutil -extract CFBundleIdentifier raw "$GUI_APP/Contents/Info.plist")" = "com.github.usadamasa.agents-daemon.agent-tasks" ]
  # go build の linker が付けた署名を剥がしてから、bundle に ad hoc 署名する (上書きの案内行を build.log に残さない)
  [ "$(wc -l <"$TEST_TMPDIR/codesign.calls")" -eq 2 ]
  [[ "$(sed -n 1p "$TEST_TMPDIR/codesign.calls")" == "--remove-signature "*"/Contents/MacOS/agent-tasks" ]]
  [[ "$(sed -n 2p "$TEST_TMPDIR/codesign.calls")" == "--force --sign - "*".agent-tasks.app."* ]]
  [ ! -d "$GUI_LOCK" ]
  [ -z "$(find "$BIN_DIR" -name '.agent-tasks.*')" ]
  [ -z "$(find "$HOME/Applications" -name '.agent-tasks.app.*')" ]
}

@test "建てた bundle の Resources にアイコンを入れ、Info.plist がそれを指す" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  cmp "$REPO_ROOT/gui/darwin/agent-tasks.icns" "$GUI_APP/Contents/Resources/agent-tasks.icns"
  [ "$(plutil -extract CFBundleIconFile raw "$GUI_APP/Contents/Info.plist")" = "agent-tasks" ]
}

@test "go build が失敗したら前のバイナリと bundle と stamp を残す" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain
  mkdir -p "$GUI_APP/Contents/MacOS" "$BIN_DIR"
  printf 'old\n' >"$GUI_APP/Contents/MacOS/agent-tasks"
  printf 'old\n' >"$GUI_BIN"
  printf '%s\n' "0000.0000.0" >"$GUI_STAMP"
  export GO_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -ne 0 ]
  [[ "$output" == *"ERROR"* ]]
  [ "$(<"$GUI_BIN")" = "old" ]
  [ "$(<"$GUI_APP/Contents/MacOS/agent-tasks")" = "old" ]
  [ "$(<"$GUI_STAMP")" = "0000.0000.0" ]
  [ ! -d "$GUI_LOCK" ]
  [ -z "$(find "$BIN_DIR" -name '.agent-tasks.*')" ]
}

@test "受け継いだ GOTOOLCHAIN=local を go build にだけ auto で上書きする" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain
  export GOTOOLCHAIN=local

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ "$(<"$TEST_TMPDIR/go.env")" = "GOTOOLCHAIN=auto" ]
}

@test "go build が失敗したら go の最後の行と建て直すコマンドを失敗印と log に残す" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain
  export GO_STUB_OUTPUT='go: download go1.99.0 for darwin/arm64: toolchain not available'
  export GO_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -ne 0 ]
  [ "$(sed -n 1p "$GUI_BUILD_FAILED")" = "go build failed: go: download go1.99.0 for darwin/arm64: toolchain not available" ]
  [[ "$(sed -n 2p "$GUI_BUILD_FAILED")" == *"! $REPO_ROOT/hooks/build-gui.sh"* ]]
  [[ "$output" == *"toolchain not available"* ]]
  [[ "$output" == *"! $REPO_ROOT/hooks/build-gui.sh"* ]]
  [ -z "$(find "$BIN_DIR" -name '.agent-tasks.*')" ]
}

@test "go build の出力を go の終了を待たずに流す" {
  require_darwin
  install_fake_gui_toolchain
  install_slow_failing_go

  PATH="$FIXTURE_PATH" "$BUILD" >"$TEST_TMPDIR/out" 2>&1 &
  local pid=$!
  wait_until file_contains "$TEST_TMPDIR/out" "downloading go1.99.0" || true
  touch "$TEST_TMPDIR/release"
  wait "$pid" || true
  [[ "$(sed -n 1p "$GUI_BUILD_FAILED")" == *"go: released" ]]
}

@test "建てられたら前の失敗印を消す" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain
  mkdir -p "$(dirname "$GUI_BUILD_FAILED")"
  printf 'go build failed: old\n! /x/hooks/build-gui.sh\n' >"$GUI_BUILD_FAILED"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ ! -e "$GUI_BUILD_FAILED" ]
}

@test "codesign が失敗したら前の bundle と stamp を残す" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain
  mkdir -p "$GUI_APP/Contents/MacOS" "$BIN_DIR"
  printf 'old\n' >"$GUI_APP/Contents/MacOS/agent-tasks"
  printf '%s\n' "0000.0000.0" >"$GUI_STAMP"
  export CODESIGN_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -ne 0 ]
  [[ "$output" == *"ERROR"* ]]
  [ "$(<"$GUI_APP/Contents/MacOS/agent-tasks")" = "old" ]
  [ "$(<"$GUI_STAMP")" = "0000.0000.0" ]
  [ ! -d "$GUI_LOCK" ]
  [ -z "$(find "$HOME/Applications" -name '.agent-tasks.app.*')" ]
}

@test "生きているプロセスが lock を持っていれば建てずに exit 0 する" {
  require_darwin
  install_fake_go
  install_fake_gui_toolchain
  mkdir -p "$GUI_LOCK"
  printf '%s\n' "$$" >"$GUI_LOCK/pid"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"another build"* ]]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ -d "$GUI_LOCK" ]
}
