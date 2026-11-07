#!/usr/bin/env bats
# build-gui.sh (依存図のアプリ agent-tasks を建てて ~/Applications に置く) を検証する。
#
# - macOS 以外か、go / cc / codesign が無ければ INFO 1 行で飛ばし、何も書かない (stamp も書かないので次の SessionStart が
#   また試す)
# - 建てたら cache のバイナリ、.app の bundle、stamp がそろい、バイナリと Info.plist に plugin の version が入る
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
  # ad hoc 署名は bundle に対して行う
  grep -q -- "--force --sign - " "$TEST_TMPDIR/codesign.calls"
  [ ! -d "$GUI_LOCK" ]
  [ -z "$(find "$BIN_DIR" -name '.agent-tasks.*')" ]
  [ -z "$(find "$HOME/Applications" -name '.agent-tasks.app.*')" ]
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
