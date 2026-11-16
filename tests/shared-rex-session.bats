#!/usr/bin/env bats
# SessionStart hook (shared-rex-session.sh) の分岐を検証する。
#
# - AGENTS_DAEMON_SHARED_REX_SESSION が無ければ何も出さず exit 0
# - あれば、rex の session を rename せず自分の window に名前を付ける案内を stdout に出す (会話の context に入る)

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  HOOK="$(cd "$BATS_TEST_DIRNAME/.." && pwd)/hooks/shared-rex-session.sh"
}

@test "共有の session で起きていなければ何も出さない" {
  unset AGENTS_DAEMON_SHARED_REX_SESSION
  run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ -z "$stderr" ]
}

@test "共有の session で起きたら session を rename しない案内を出す" {
  AGENTS_DAEMON_SHARED_REX_SESSION=1 run_hook --separate-stderr "$HOOK"
  [ "$status" -eq 0 ]
  [[ "$output" == *"rex session rename"* ]]
  [[ "$output" == *"rex window rename"* ]]
  [ -z "$stderr" ]
}
