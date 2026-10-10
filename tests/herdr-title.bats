#!/usr/bin/env bats
# UserPromptSubmit / Stop hook (herdr-title.sh) を検証する｡
#
# hook の仕事は stdin をバイナリの `herdr-title` へ渡すことだけで､herdr の pane の判定と socket の呼び出しは
# Go 側 (internal/hookreport) のテストが持つ｡ここで見るのは
# - stdin を加工せずに渡すか
# - バイナリの stdout を context に漏らさないか (stderr へ寄せるか)
# - バイナリが無い / 失敗した (サブコマンドを持たない古いバイナリも含む) ときに exit 0 で戻るか

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/herdr-title.sh"
  INPUT='{"session_id":"sess-1","transcript_path":"/tmp/t.jsonl","hook_event_name":"Stop"}'
}

teardown() {
  teardown_daemon_fixture
}

@test "バイナリがあれば stdin をそのまま herdr-title へ渡し､stdout を context に漏らさない" {
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >>"%s"\nprintf "%%s\\n" "$(</dev/stdin)" >"%s"\nprintf "leak\\n"\n' \
    "$TEST_TMPDIR/daemon.calls" "$TEST_TMPDIR/daemon.stdin" >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [[ "$stderr" == *"leak"* ]]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "herdr-title" ]
  [ "$(<"$TEST_TMPDIR/daemon.stdin")" = "$INPUT" ]
}

@test "バイナリが失敗しても (サブコマンドの無い古いバイナリなど) exit 0 で戻る" {
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "unknown command\\n" >&2\nexit 1\n' >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [[ "$stderr" == *"herdr-title failed (rc=1)"* ]]
}

@test "バイナリが無ければ何もせず exit 0 で戻る" {
  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<"$INPUT"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [[ "$stderr" == *"SKIP"* ]]
}
