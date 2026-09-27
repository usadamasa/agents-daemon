#!/usr/bin/env bats
# Stop hook (cache-state.sh) を検証する｡
#
# hook の仕事は stdin をバイナリの `ingest-stop` へ渡すことだけで､transcript の読み取りと
# sidecar の書式は Go 側 (internal/sessionstate) のテストが持つ｡ここで見るのは
# - stdin を加工せずに渡すか
# - バイナリが無い (SessionStart の build 前) / 失敗しても exit 0 で stdout を空に保つか
# Stop hook の stdout は Claude Code が判定 JSON として読むため､どの分岐でも空でなければならない｡

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/cache-state.sh"
  INPUT='{"session_id":"sess-1","transcript_path":"/tmp/t.jsonl","stop_hook_active":false}'
}

teardown() {
  teardown_daemon_fixture
}

# 呼ばれた引数を $1 へ､stdin を $2 へ書くだけの daemon バイナリを $BIN に置く｡
make_recording_daemon() {
  local args="$1" stdin="$2"
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >>"%s"\ncat >"%s"\n' "$args" "$stdin" >"$BIN"
  chmod +x "$BIN"
}

@test "バイナリがあれば stdin をそのまま ingest-stop へ渡す" {
  make_recording_daemon "$TEST_TMPDIR/daemon.calls" "$TEST_TMPDIR/daemon.stdin"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "ingest-stop" ]
  [ "$(<"$TEST_TMPDIR/daemon.stdin")" = "$INPUT" ]
}

@test "バイナリが無ければ何もせず exit 0 で戻る" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ ! -d "$XDG_STATE_HOME/agents-daemon/cache" ]
}

@test "バイナリが失敗しても exit 0 で stdout は空のまま" {
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "boom\\n"\nprintf "reason\\n" >&2\nexit 1\n' >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [[ "$stderr" == *"reason"* ]]
}
