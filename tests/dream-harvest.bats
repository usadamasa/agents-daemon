#!/usr/bin/env bats
# Stop hook (dream-harvest.sh) の分岐を検証する。
#
# - cache のバイナリが無ければ何もせず exit 0 (SessionStart hook が build するまで待つ)
# - あれば `dream harvest --incremental --detach` を exec する。detach とログはバイナリ側の責務
#   (cmd/agents-daemon/internal/dreamcli/dream_test.go、internal/dream/harvest/detach_test.go)
#
# Stop hook の stdout は会話の context へ注入されるため、どの分岐でも stdout を空に保つ。

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/dream-harvest.sh"
}

teardown() {
  teardown_daemon_fixture
}

@test "バイナリが無ければ何もせず exit 0 (stdout も stderr も空)" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ -z "$stderr" ]
}

@test "バイナリがあれば dream harvest --incremental --detach を exec し、stdout は空" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "dream harvest --incremental --detach" ]
}

@test "バイナリの stdout は stderr へ寄せる (context を汚さない)" {
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "to stdout\\n"\n' >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$stderr" = "to stdout" ]
}
