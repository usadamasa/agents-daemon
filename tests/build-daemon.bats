#!/usr/bin/env bats
# build-daemon.sh の排他と失敗時の後始末を検証する。
#
# 複数セッションの SessionStart が同時に build を起こしても二重に建てない (mkdir の lock)。
# go build が失敗したら cache のバイナリと stamp を触らず、lock も残さない。

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  BUILD="$REPO_ROOT/hooks/build-daemon.sh"
  install_fake_go
}

teardown() {
  teardown_daemon_fixture
}

@test "生きているプロセスが lock を持っていれば build せずに exit 0 し、その lock を消さない" {
  mkdir -p "$LOCK"
  printf '%s\n' "$$" >"$LOCK/pid"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"another build"* ]]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ ! -e "$BIN" ]
  [ -d "$LOCK" ]
  [ "$(<"$LOCK/pid")" = "$$" ]
}

@test "持ち主が死んだ lock は残骸として消し、build を進める" {
  # 既に終了したプロセスの PID を持ち主にする (SIGKILL で cleanup が走らなかった状態)。
  local dead_pid
  dead_pid=$(bash -c 'printf "%s" "$$"')
  mkdir -p "$LOCK"
  printf '%s\n' "$dead_pid" >"$LOCK/pid"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"stale lock"* ]]
  [ -x "$BIN" ]
  [ "$(<"$STAMP")" = "$PLUGIN_VERSION" ]
  [ ! -d "$LOCK" ]
}

@test "pid ファイルの無い lock も残骸として扱う" {
  mkdir -p "$LOCK"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"stale lock"* ]]
  [ -x "$BIN" ]
  [ ! -d "$LOCK" ]
}

@test "daemon を建てて起動した後に agent-tasks も建てる" {
  require_darwin
  install_fake_gui_toolchain

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ "$(<"$STAMP")" = "$PLUGIN_VERSION" ]
  [ "$(<"$GUI_STAMP")" = "$PLUGIN_VERSION" ]
  [ -x "$GUI_APP/Contents/MacOS/agent-tasks" ]
  # daemon --ensure が gui の build より先
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
  grep -q -- " ./cmd/agents-daemon" "$TEST_TMPDIR/go.calls"
  grep -q -- "build -C $REPO_ROOT/gui " "$TEST_TMPDIR/go.calls"
}

@test "agent-tasks の build が失敗しても、daemon は建てて起動し、exit 0 で ERROR を残す" {
  require_darwin
  install_fake_gui_toolchain
  export CODESIGN_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [[ "$output" == *"ERROR"*"agent-tasks build failed"* ]]
  [ "$(<"$STAMP")" = "$PLUGIN_VERSION" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
  [ ! -e "$GUI_STAMP" ]
  [ ! -d "$LOCK" ]
  [ ! -d "$GUI_LOCK" ]
}

@test "toolchain が無ければ daemon だけ建てて、agent-tasks は INFO で飛ばす" {
  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ "$(<"$STAMP")" = "$PLUGIN_VERSION" ]
  [[ "$output" == *"skipping agent-tasks"* ]]
  [ ! -e "$GUI_STAMP" ]
}

@test "go build が失敗したら既存のバイナリと stamp を残し、一時ファイルと lock を片付ける" {
  make_fake_daemon "$TEST_TMPDIR/old-daemon.calls" "$BIN"
  printf '%s\n' "0000.0000.0" >"$STAMP"
  cp "$BIN" "$TEST_TMPDIR/old-bin"
  export GO_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -ne 0 ]
  [[ "$output" == *"ERROR"* ]]
  cmp -s "$BIN" "$TEST_TMPDIR/old-bin"
  [ "$(<"$STAMP")" = "0000.0000.0" ]
  [ ! -e "$TEST_TMPDIR/old-daemon.calls" ]
  [ ! -d "$LOCK" ]
  [ -z "$(find "$BIN_DIR" -name '.agents-daemon.*')" ]
}

# セッションを開いたリポジトリの devShell が付けた GOTOOLCHAIN=local を受け継ぐと、PATH の go が go.mod より古いとき
# toolchain を取りに行けずに落ちる。auto は go build にだけ渡し、起動する daemon には渡さない。
@test "受け継いだ GOTOOLCHAIN=local を go build にだけ auto で上書きする" {
  export GOTOOLCHAIN=local

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ "$(<"$TEST_TMPDIR/go.env")" = "GOTOOLCHAIN=auto" ]
  [ "$(<"$TEST_TMPDIR/daemon.env")" = "GOTOOLCHAIN=local" ]
}

@test "go build が失敗したら go の最後の行と建て直すコマンドを失敗印と log に残す" {
  export GO_STUB_OUTPUT=$'go: downloading go1.99.0 (darwin/arm64)\ngo: download go1.99.0 for darwin/arm64: toolchain not available'
  export GO_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -ne 0 ]
  [ "$(sed -n 1p "$BUILD_FAILED")" = "go build failed: go: download go1.99.0 for darwin/arm64: toolchain not available" ]
  [[ "$(sed -n 2p "$BUILD_FAILED")" == *"! $REPO_ROOT/hooks/build-daemon.sh"* ]]
  # 失敗したセッションでは systemMessage がまだ出ないので、log (この出力) にも同じ 2 行を残す
  [[ "$output" == *"toolchain not available"* ]]
  [[ "$output" == *"! $REPO_ROOT/hooks/build-daemon.sh"* ]]
  [ -z "$(find "$BIN_DIR" -name '.agents-daemon.*')" ]
}

@test "go build が何も出さずに失敗しても、失敗印の 1 行目は空にしない" {
  export GO_STUB_EXIT=1

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -ne 0 ]
  [ "$(sed -n 1p "$BUILD_FAILED")" = "go build failed" ]
}

# `!` で建て直すとき、toolchain の取得中の表示が go の終了を待たずに見えること。
@test "go build の出力を go の終了を待たずに流す" {
  install_slow_failing_go

  PATH="$FIXTURE_PATH" "$BUILD" >"$TEST_TMPDIR/out" 2>&1 &
  local pid=$!
  wait_until file_contains "$TEST_TMPDIR/out" "downloading go1.99.0" || true
  touch "$TEST_TMPDIR/release"
  wait "$pid" || true
  [[ "$(sed -n 1p "$BUILD_FAILED")" == *"go: released" ]]
}

@test "建てられたら前の失敗印を消す" {
  mkdir -p "$(dirname "$BUILD_FAILED")"
  printf 'go build failed: old\n! /x/hooks/build-daemon.sh\n' >"$BUILD_FAILED"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ ! -e "$BUILD_FAILED" ]
}
