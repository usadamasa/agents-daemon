#!/usr/bin/env bats
# SessionStart hook (ensure-daemon.sh) の分岐を検証する｡
#
# - cache のバイナリと stamp が plugin の version と揃っていれば daemon --ensure を exec する
# - 揃っていなければ build-daemon.sh を detach して即座に戻る (go build を前景で回さない)
# - go が無ければ build.log に ERROR を残し、バイナリを作らない
#
# SessionStart hook の stdout は会話の context へ注入されるため、どの分岐でも stdout を空に保つ｡

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
  install_fake_go

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "daemon --ensure" ]
  [ ! -e "$TEST_TMPDIR/go.calls" ]
  [ ! -e "$BUILD_LOG" ]
}

@test "stamp が plugin の version と違えば build を detach し、完了後に新しいバイナリで daemon --ensure する" {
  make_fake_daemon "$TEST_TMPDIR/old-daemon.calls" "$BIN"
  printf '%s\n' "0000.0000.0" >"$STAMP"
  install_fake_go
  # fake go は release が置かれるまで終わらない｡hook がそれを待たずに戻ることを確かめる｡
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
  grep -q -- "build -C $REPO_ROOT -o " "$TEST_TMPDIR/go.calls"
  grep -q -- " ./cmd/agents-daemon" "$TEST_TMPDIR/go.calls"
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

@test "go が PATH に無ければ build.log に ERROR を残し、バイナリを作らない" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]

  wait_until build_failed
  grep -q 'ERROR.*go not found in PATH' "$BUILD_LOG"
  [ ! -e "$BIN" ]
  [ ! -e "$STAMP" ]
}
