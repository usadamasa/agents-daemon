#!/usr/bin/env bats
# build-daemon.sh の排他と失敗時の後始末を検証する｡
#
# 複数セッションの SessionStart が同時に build を起こしても二重に建てない (mkdir の lock)｡
# go build が失敗したら cache のバイナリと stamp を触らず､lock も残さない｡

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

@test "生きているプロセスが lock を持っていれば build せずに exit 0 し､その lock を消さない" {
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

@test "持ち主が死んだ lock は残骸として消し､build を進める" {
  # 既に終了したプロセスの PID を持ち主にする (SIGKILL で cleanup が走らなかった状態)｡
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

@test "go build が失敗したら既存のバイナリと stamp を残し､一時ファイルと lock を片付ける" {
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
