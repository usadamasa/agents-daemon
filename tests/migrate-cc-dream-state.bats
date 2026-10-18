#!/usr/bin/env bats
# 一回きりの移行スクリプト migrate-cc-dream-state.sh を検証する。
#
# 旧 cc-dream の state (~/.local/state/cc-dream) を agents-daemon/dream/ へ mv し、旧ディレクトリを消す。
# 中身を黙って混ぜない: 移す先に同じ名前があれば何もせずに失敗する。

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  SCRIPT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)/scripts/migrate-cc-dream-state.sh"
  TEST_TMPDIR=$(mktemp -d "${TMPDIR:-/tmp}/agents-daemon-test.XXXXXX")
  export HOME="$TEST_TMPDIR/home"
  export XDG_STATE_HOME="$TEST_TMPDIR/state"
  OLD="$XDG_STATE_HOME/cc-dream"
  NEW="$XDG_STATE_HOME/agents-daemon/dream"
  mkdir -p "$HOME"
}

teardown() {
  rm -rf "$TEST_TMPDIR"
}

# 旧 cc-dream が書いていた形の state を作る。
make_old_state() {
  mkdir -p "$OLD/harvest" "$OLD/candidates" "$OLD/replays" "$OLD/verdicts"
  printf '{"session_id":"s1"}\n' >"$OLD/harvest/2026-10-01.jsonl"
  printf '{"last_run_ts":"2026-10-17T00:00:00+09:00"}\n' >"$OLD/harvest-state.json"
  printf '{"id":"c1"}\n' >"$OLD/candidates/p-2026-10-01.jsonl"
  printf '{}\n' >"$OLD/replays/c1.json"
  printf '{}\n' >"$OLD/verdicts/c1.json"
  printf 'old log line\n' >"$OLD/harvest.log"
}

@test "旧 state を agents-daemon/dream へ移し、harvest.log を捨て、旧ディレクトリを消す" {
  make_old_state

  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 0 ]

  [ -f "$NEW/harvest/2026-10-01.jsonl" ]
  [ -f "$NEW/harvest-state.json" ]
  [ -f "$NEW/candidates/p-2026-10-01.jsonl" ]
  [ -f "$NEW/replays/c1.json" ]
  [ -f "$NEW/verdicts/c1.json" ]
  [ ! -e "$NEW/harvest.log" ]
  [ ! -e "$OLD" ]
  [[ "$output" == *"$NEW"* ]]
}

@test "旧ディレクトリが無ければ何もせず exit 0 で、その旨を出す" {
  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"移行するものはありません"* ]]
  [ ! -e "$NEW" ]
}

@test "移す先に同じ名前があれば何も動かさずに失敗する" {
  make_old_state
  mkdir -p "$NEW/harvest"
  printf '{"session_id":"new"}\n' >"$NEW/harvest/2026-10-18.jsonl"

  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"harvest"* ]]
  # 旧も新も手を付けていない
  [ -f "$OLD/harvest/2026-10-01.jsonl" ]
  [ -f "$OLD/harvest-state.json" ]
  [ -f "$NEW/harvest/2026-10-18.jsonl" ]
  [ ! -e "$NEW/harvest-state.json" ]
}

@test "知らないエントリが旧ディレクトリに残っていれば失敗し、何を見ればよいか出す" {
  make_old_state
  printf 'x\n' >"$OLD/unknown.txt"

  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"unknown.txt"* ]]
  [ -f "$OLD/harvest-state.json" ]
  [ ! -e "$NEW/harvest-state.json" ]
}

@test "XDG_STATE_HOME が無ければ ~/.local/state を使う" {
  unset XDG_STATE_HOME
  OLD="$HOME/.local/state/cc-dream"
  NEW="$HOME/.local/state/agents-daemon/dream"
  make_old_state

  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 0 ]
  [ -f "$NEW/harvest-state.json" ]
  [ ! -e "$OLD" ]
}
