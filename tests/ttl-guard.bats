#!/usr/bin/env bats
# UserPromptSubmit hook (ttl-guard.sh) を検証する｡
#
# hook の仕事は stdin をバイナリの `ttl-guard` へ渡し､止めるか (exit 2) だけを伝えることで､
# transcript の読み取りと ack の規則は Go 側 (internal/sessionstate) のテストが持つ｡ここで見るのは
# - stdin を加工せずに渡すか
# - バイナリの exit 2 と警告 (stderr) をそのまま伝えるか
# - バイナリが無い / 失敗した (サブコマンドを持たない古いバイナリも含む) ときに止めないか
# UserPromptSubmit の stdout は exit 0 で context に入るため､どの分岐でも空でなければならない｡

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/ttl-guard.sh"
  INPUT='{"session_id":"sess-1","transcript_path":"/tmp/t.jsonl","prompt":"hello"}'
}

teardown() {
  teardown_daemon_fixture
}

# stdout と stderr に 1 行ずつ出して $1 で終わる daemon バイナリを $BIN に置く｡
make_exiting_daemon() {
  local code="$1"
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "out\\n"\nprintf "warning\\n" >&2\nexit %s\n' "$code" >"$BIN"
  chmod +x "$BIN"
}

@test "バイナリがあれば stdin をそのまま ttl-guard へ渡す" {
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >>"%s"\nprintf "%%s\\n" "$(</dev/stdin)" >"%s"\n' \
    "$TEST_TMPDIR/daemon.calls" "$TEST_TMPDIR/daemon.stdin" >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "ttl-guard" ]
  [ "$(<"$TEST_TMPDIR/daemon.stdin")" = "$INPUT" ]
}

@test "バイナリが exit 2 なら exit 2 で警告を stderr に残す" {
  make_exiting_daemon 2

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 2 ]
  [ -z "$output" ]
  [[ "$stderr" == *"warning"* ]]
}

@test "バイナリが exit 1 (サブコマンドの無い古いバイナリなど) なら止めない" {
  make_exiting_daemon 1

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "バイナリが無ければ何もせず exit 0 で戻る" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}
