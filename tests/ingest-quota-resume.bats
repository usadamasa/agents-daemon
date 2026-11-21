#!/usr/bin/env bats
# Notification hook (ingest-quota-resume.sh) の分岐を検証する。
#
# - cache のバイナリか stamp が無ければ何もせず exit 0
# - stamp が plugin の version より古ければ、両方の version を stderr に 1 行出して exit 0 (旧版のバイナリは
#   このサブコマンドを知らない)
# - stamp が plugin の version 以上なら `ingest-quota-resume` を exec し、stdin をそのまま渡す。どの通知を記録するかは
#   バイナリ側の責務 (quotaresume、daemoncli/ingest_quota_resume_test.go)

bats_require_minimum_version 1.5.0

INPUT='{"hook_event_name": "Notification", "notification_type": "quota_auto_resume_stale"}'

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/ingest-quota-resume.sh"
}

teardown() {
  teardown_daemon_fixture
}

@test "バイナリが無ければ何もせず exit 0 (stdout も stderr も空)" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ -z "$stderr" ]
}

@test "stamp が plugin の version より古ければ旧版のバイナリを呼ばず、両方の version を stderr に出す" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"
  printf '0000.0000.0\n' >"$STAMP"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ ! -f "$TEST_TMPDIR/daemon.calls" ]
  [[ "$stderr" == *"0000.0000.0"* ]]
  [[ "$stderr" == *"$PLUGIN_VERSION"* ]]
}

@test "stamp が無ければ何もせず exit 0" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ ! -f "$TEST_TMPDIR/daemon.calls" ]
}

@test "バイナリと stamp がそろっていれば ingest-quota-resume を exec し、stdin をそのまま渡す" {
  mkdir -p "$BIN_DIR"
  printf '%s\n' "$PLUGIN_VERSION" >"$STAMP"
  # FIXTURE_PATH に cat は無いので builtin の read で 1 行受ける
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >"%s"\nIFS= read -r line; printf "%%s\\n" "$line" >"%s"\n' \
    "$TEST_TMPDIR/daemon.calls" "$TEST_TMPDIR/daemon.stdin" >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "ingest-quota-resume" ]
  [ "$(<"$TEST_TMPDIR/daemon.stdin")" = "$INPUT" ]
}
