#!/usr/bin/env bats
# build-daemon.sh の排他と失敗時の後始末を検証する｡
#
# 複数セッションの SessionStart が同時に build を起こしても二重に建てない (mkdir の lock)｡
# go build が失敗したら cache のバイナリと stamp を触らず、lock も残さない｡

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

@test "lock が既にあれば build せずに exit 0 し、他の build の lock を消さない" {
  mkdir -p "$LOCK"

  PATH="$FIXTURE_PATH" run_hook "$BUILD"
  [ "$status" -eq 0 ]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ ! -e "$BIN" ]
  [ -d "$LOCK" ]
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
