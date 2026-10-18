#!/usr/bin/env bats
# SessionStart hook (prune-worktrees.sh) の分岐を検証する。
#
# - cache のバイナリが無ければ何もせず exit 0 (ensure-daemon.sh が build するまで待つ)
# - あれば `prune-worktrees --hook` を exec し、stdin をそのまま渡す。判定と systemMessage はバイナリ側の責務
#   (cmd/agents-daemon/cmd_prune_worktrees_test.go、internal/worktree)
#
# dream-harvest.sh と違い、この hook は stdout を stderr へ寄せない。バイナリが出す {"systemMessage": ...} を
# Claude Code に読ませるため。

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  load lib/daemon-fixtures
  setup_daemon_fixture
  HOOK="$REPO_ROOT/hooks/prune-worktrees.sh"
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

@test "バイナリがあれば prune-worktrees --hook を exec する" {
  make_fake_daemon "$TEST_TMPDIR/daemon.calls" "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"cwd": "/nowhere"}'
  [ "$status" -eq 0 ]
  [ "$(<"$TEST_TMPDIR/daemon.calls")" = "prune-worktrees --hook" ]
}

@test "stdin の hook 入力をバイナリにそのまま渡す" {
  mkdir -p "$BIN_DIR"
  # FIXTURE_PATH に cat は無いので builtin の read で 1 行受ける
  printf '#!/bin/bash\nIFS= read -r line; printf "%%s\\n" "$line" >"%s"\n' "$TEST_TMPDIR/daemon.stdin" >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK" <<<'{"cwd": "/nowhere"}'
  [ "$status" -eq 0 ]
  [ "$(<"$TEST_TMPDIR/daemon.stdin")" = '{"cwd": "/nowhere"}' ]
}

@test "バイナリの stdout をそのまま通す (systemMessage を Claude Code に読ませる)" {
  mkdir -p "$BIN_DIR"
  printf '#!/bin/bash\nprintf "%%s\\n" "{\\"systemMessage\\": \\"回収しました\\"}"\n' >"$BIN"
  chmod +x "$BIN"

  PATH="$FIXTURE_PATH" run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ "$output" = '{"systemMessage": "回収しました"}' ]
  [ -z "$stderr" ]
}
