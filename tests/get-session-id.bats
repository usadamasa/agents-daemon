#!/usr/bin/env bats
# compact-prep skill が state file の名前に使う session_id の取得を検証する｡
#
# 値はファイル名になるため、環境変数が無いときやパス区切りを含むときは推測せずに失敗させる｡

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  SCRIPT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)/scripts/get-session-id.sh"
}

@test "CLAUDE_CODE_SESSION_ID が無ければ exit 1 し、何も出さない" {
  unset CLAUDE_CODE_SESSION_ID
  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 1 ]
  [ -z "$output" ]
  [[ "$stderr" == *"not set"* ]]
}

@test "パス区切りを含む値は exit 1 で弾く" {
  CLAUDE_CODE_SESSION_ID="../../etc/passwd" run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 1 ]
  [ -z "$output" ]
  [[ "$stderr" == *"unexpected form"* ]]
}

@test "UUID 形の値はそのまま出す" {
  CLAUDE_CODE_SESSION_ID="0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b" run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 0 ]
  [ "$output" = "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b" ]
}
