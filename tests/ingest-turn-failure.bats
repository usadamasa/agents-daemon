#!/usr/bin/env bats
# StopFailure / Stop / UserPromptSubmit hook (ingest-turn-failure.sh) の分岐を検証する。
#
# - cache のバイナリか stamp が無ければ何もせず exit 0
# - stamp が plugin の version より古ければ、両方の version を stderr に 1 行出して exit 0 (旧版のバイナリは
#   このサブコマンドを知らないかもしれない)
# - stamp が plugin の version 以上なら `ingest-turn-failure` を exec し、stdin をそのまま渡す。どの event で書くか消すかは
#   バイナリ側の責務 (turnfailure、daemoncli/ingest_turn_failure_test.go)

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/ingest-turn-failure.sh"
}

teardown() {
  teardown_daemon_fixture
}

# stamp を plugin の version に揃える (バイナリが今の plugin から建ったことにする)
write_current_stamp() {
  mkdir -p "$BIN_DIR"
  printf '%s\n' "$PLUGIN_VERSION" >"$STAMP"
}

@test "バイナリが無ければ何もせず exit 0 (stdout も stderr も空)" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ -z "$stderr" ]
}

@test "stamp が plugin の version より古ければ旧版のバイナリを呼ばず、両方の version を stderr に出す" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '0000.0000.0\n' >"$STAMP"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"hook_event_name": "StopFailure"}'
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ ! -f "$TEST_TMPDIR/daemon.calls" ]
  [[ "$stderr" == *"0000.0000.0"* ]]
  [[ "$stderr" == *"$PLUGIN_VERSION"* ]]
}

@test "stamp が plugin の version より新しければ ingest-turn-failure を exec する" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '9999.9999.9\n' >"$STAMP"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"hook_event_name": "StopFailure"}'
  [ "$status" -eq 0 ]
  [ -z "$stderr" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "ingest-turn-failure" ]
}

@test "stamp が読めなければバイナリを呼ばず、cannot compare を stderr に出す" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf 'garbage\n' >"$STAMP"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"hook_event_name": "StopFailure"}'
  [ "$status" -eq 0 ]
  [ ! -f "$TEST_TMPDIR/daemon.calls" ]
  [[ "$stderr" == *"cannot compare"* ]]
}

@test "stamp が無ければ何もせず exit 0" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"hook_event_name": "StopFailure"}'
  [ "$status" -eq 0 ]
  [ ! -f "$TEST_TMPDIR/daemon.calls" ]
}

@test "バイナリと stamp がそろっていれば ingest-turn-failure を exec する" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  write_current_stamp

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"hook_event_name": "StopFailure"}'
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "ingest-turn-failure" ]
}

@test "stdin の hook 入力をバイナリにそのまま渡す" {
  write_current_stamp
  # FIXTURE_PATH に cat は無いので builtin の read で 1 行受ける
  printf '#!/bin/bash\nIFS= read -r line; printf "%%s\\n" "$line" >"%s"\n' "$TEST_TMPDIR/daemon.stdin" >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"hook_event_name": "StopFailure", "error": "rate_limit"}'
  [ "$status" -eq 0 ]
  [ "$(<"$TEST_TMPDIR/daemon.stdin")" = '{"hook_event_name": "StopFailure", "error": "rate_limit"}' ]
}
