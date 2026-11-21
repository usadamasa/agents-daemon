#!/usr/bin/env bats
# scripts/release-verify.sh のテスト。一時の git repo と fixture の複製元、go と daemon の代役で回す。
# 実データと本物の rex には触れない。

setup() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  SCRIPT="$REPO_ROOT/scripts/release-verify.sh"
  TEST_TMPDIR=$(mktemp -d "${TMPDIR:-/tmp}/release-verify-test.XXXXXX")
  TEST_TMPDIR=$(cd "$TEST_TMPDIR" && pwd -P)
  export TEST_TMPDIR

  # 検証の対象の repo。gui/ と mod は、人の確認が要るかの判定に使う。
  export VERIFY_REPO="$TEST_TMPDIR/repo"
  mkdir -p "$VERIFY_REPO/gui" "$VERIFY_REPO/hooks" "$VERIFY_REPO/cmd"
  printf 'a\n' >"$VERIFY_REPO/cmd/main.go"
  printf 'a\n' >"$VERIFY_REPO/gui/main.go"
  printf 'a\n' >"$VERIFY_REPO/hooks/register.ts"
  git -C "$VERIFY_REPO" init -q -b main
  git -C "$VERIFY_REPO" config user.email test@example.invalid
  git -C "$VERIFY_REPO" config user.name test
  git -C "$VERIFY_REPO" config commit.gpgsign false
  git -C "$VERIFY_REPO" config tag.gpgsign false
  git -C "$VERIFY_REPO" add .
  git -C "$VERIFY_REPO" commit -q -m base
  git -C "$VERIFY_REPO" tag 1.0.0

  # 複製元。allowlist の中と外を 1 つずつ置く。
  export SRC_STATE="$TEST_TMPDIR/src/state"
  export SRC_CLAUDE="$TEST_TMPDIR/src/claude"
  export SRC_CONFIG="$TEST_TMPDIR/src/config.json"
  mkdir -p "$SRC_STATE/work" "$SRC_STATE/edges" "$SRC_STATE/spawned-sessions" "$SRC_STATE/logs"
  printf '{}\n' >"$SRC_STATE/work/s1.json"
  printf 'x\n' >"$SRC_STATE/work/.lock"
  printf '{}\n' >"$SRC_STATE/edges/e1.json"
  printf '{}\n' >"$SRC_STATE/spawned-sessions/x.json"
  printf 'log\n' >"$SRC_STATE/logs/daemon.log"
  printf '1\n' >"$SRC_STATE/daemon.pid"
  mkdir -p "$SRC_CLAUDE/projects/p1/memory" "$SRC_CLAUDE/skills" "$SRC_CLAUDE/plugins" "$TEST_TMPDIR/src/skill-origin"
  printf 'memory\n' >"$SRC_CLAUDE/projects/p1/memory/MEMORY.md"
  printf 'transcript\n' >"$SRC_CLAUDE/projects/p1/session.jsonl"
  printf '{}\n' >"$SRC_CLAUDE/plugins/installed_plugins.json"
  printf 'skill\n' >"$TEST_TMPDIR/src/skill-origin/SKILL.md"
  ln -s "$TEST_TMPDIR/src/skill-origin" "$SRC_CLAUDE/skills/linked"
  printf 'global\n' >"$SRC_CLAUDE/CLAUDE.md"
  printf '{"enabled":true}\n' >"$SRC_CONFIG"

  export VERIFY_BASE="$TEST_TMPDIR/base"
  export VERIFY_GUI=skip
  export VERIFY_WRITE_GUARD=none
  export VERIFY_DAEMON_SECONDS=1
  export VERIFY_BASELINE_BIN="$TEST_TMPDIR/no-baseline"
  export VERIFY_REX_SOCKET="$TEST_TMPDIR/rex.sock"
  printf '' >"$VERIFY_REX_SOCKET"
  # 呼び手の env に載っている rex の変数が、隔離の env へ漏れないことを確かめる。
  export REX_SESSION="session:caller"
  export REX_BLOCK="block:caller"

  export FAKE_VERIFY_DAEMON="$REPO_ROOT/tests/fixtures/fake-verify-daemon"
  mkdir -p "$TEST_TMPDIR/pathbin"
  cp "$REPO_ROOT/tests/fixtures/fake-verify-go" "$TEST_TMPDIR/pathbin/go"
  chmod +x "$TEST_TMPDIR/pathbin/go"
  PATH="$TEST_TMPDIR/pathbin:$PATH"
}

teardown() {
  if [ -n "${TEST_TMPDIR:-}" ]; then
    rm -rf "$TEST_TMPDIR"
  fi
}

# REF (既定 HEAD) の tree から決まる環境の root。
env_root() {
  local tree
  tree=$(git -C "$VERIFY_REPO" rev-parse "${1:-HEAD}^{tree}")
  printf '%s/%s\n' "$VERIFY_BASE" "${tree:0:12}"
}

# gui/ と mod に触れない commit を 1 つ足す。
commit_cmd_change() {
  printf '%s\n' "$1" >"$VERIFY_REPO/cmd/main.go"
  git -C "$VERIFY_REPO" commit -q -am "$1"
}

@test "up: allowlist の中だけを写し、symlink は辿らない" {
  run "$SCRIPT" up
  [ "$status" -eq 0 ]
  root=$(env_root)
  state="$root/home/.local/state/agents-daemon"
  claude="$root/home/.claude"
  [ -f "$root/.agents-daemon-verify" ]
  [ -f "$state/work/s1.json" ]
  [ -f "$state/edges/e1.json" ]
  [ ! -e "$state/work/.lock" ]
  [ ! -e "$state/spawned-sessions" ]
  [ ! -e "$state/logs" ]
  [ ! -e "$state/daemon.pid" ]
  [ -f "$claude/projects/p1/memory/MEMORY.md" ]
  [ ! -e "$claude/projects/p1/session.jsonl" ]
  [ -f "$claude/plugins/installed_plugins.json" ]
  [ -L "$claude/skills/linked" ]
  [ -f "$claude/CLAUDE.md" ]
  [ -f "$root/home/.config/agents-daemon/config.json" ]
}

@test "up: 作業ツリーではなく commit から建てる" {
  printf 'uncommitted\n' >"$VERIFY_REPO/cmd/extra.go"
  run "$SCRIPT" up
  [ "$status" -eq 0 ]
  root=$(env_root)
  [ -f "$root/src/cmd/main.go" ]
  [ ! -e "$root/src/cmd/extra.go" ]
  grep -qF -- "-C $root/src" "$TEST_TMPDIR/go.calls"
  [ -x "$root/home/.cache/agents-daemon/bin/agents-daemon" ]
}

@test "up: 同じ tree の環境が残っていれば拒む" {
  run "$SCRIPT" up
  [ "$status" -eq 0 ]
  run "$SCRIPT" up
  [ "$status" -ne 0 ]
}

@test "up: 複製元が環境の置き場の下にあれば拒む" {
  export VERIFY_BASE="$TEST_TMPDIR/src"
  run "$SCRIPT" up
  [ "$status" -eq 2 ]
  [[ "$output" == *"複製元が環境の置き場の下にある"* ]]
  [ ! -e "$(env_root)" ]
}

@test "check: CLI は隔離の env で呼ばれ、呼び手の rex の変数を継がない" {
  "$SCRIPT" up
  run "$SCRIPT" check
  [ "$status" -eq 0 ]
  root=$(env_root)
  line=$(grep '^deps|' "$TEST_TMPDIR/daemon.calls")
  [[ "$line" == *"|HOME=$root/home|"* ]]
  [[ "$line" == *"|XDG_STATE_HOME=$root/home/.local/state|"* ]]
  [[ "$line" == *"|CLAUDE_CONFIG_DIR=$root/home/.claude|"* ]]
  [[ "$line" == *"|REX_SERVER=unix://$root/rex-dead.sock|"* ]]
  [[ "$line" == *"|REX_SESSION=|REX_BLOCK=" ]]
}

@test "check: daemon は dry-run で起き、本物の rex と Claude の設定を読む" {
  "$SCRIPT" up
  run "$SCRIPT" check
  [ "$status" -eq 0 ]
  root=$(env_root)
  line=$(grep '^daemon ' "$TEST_TMPDIR/daemon.calls")
  [[ "$line" == "daemon --foreground --dry-run|"* ]]
  [[ "$line" == *"|XDG_STATE_HOME=$root/home/.local/state|"* ]]
  [[ "$line" == *"|CLAUDE_CONFIG_DIR=$SRC_CLAUDE|"* ]]
  [[ "$line" == *"|REX_SERVER=unix://$VERIFY_REX_SOCKET|"* ]]
  [[ "$line" == *"|REX_BLOCK=" ]]
  [[ "$line" != *"session:caller"* ]]
}

@test "check: リリースする tree の skill を dream check に掛け、指摘があれば落ちる" {
  mkdir -p "$VERIFY_REPO/skills/s1"
  printf 'skill\n' >"$VERIFY_REPO/skills/s1/SKILL.md"
  git -C "$VERIFY_REPO" add skills
  git -C "$VERIFY_REPO" commit -q -m skill
  "$SCRIPT" up
  root=$(env_root)
  run "$SCRIPT" check
  [ "$status" -eq 0 ]
  grep -qF -- "--skills-dir $root/src/skills|" "$TEST_TMPDIR/daemon.calls"

  printf '' >"$TEST_TMPDIR/fail-own-skills"
  run "$SCRIPT" check
  [ "$status" -eq 1 ]
  [[ "$output" == *"[fail] dream-check-skills"* ]]
}

@test "check: rex の socket が無ければ落ちる" {
  rm "$VERIFY_REX_SOCKET"
  "$SCRIPT" up
  run "$SCRIPT" check
  [ "$status" -ne 0 ]
}

@test "run: 通ると記録を書き、環境を消す" {
  commit_cmd_change b
  run "$SCRIPT" run
  [ "$status" -eq 0 ]
  [ ! -e "$(env_root)" ]
  run "$SCRIPT" status
  [ "$status" -eq 0 ]
  [ "$(jq -r .result <<<"$output")" = pass ]
  [ "$(jq -r .tree <<<"$output")" = "$(git -C "$VERIFY_REPO" rev-parse 'HEAD^{tree}')" ]
}

@test "run: check が落ちると記録を書かず、環境を残す" {
  commit_cmd_change b
  printf '' >"$TEST_TMPDIR/fail-deps"
  run "$SCRIPT" run
  [ "$status" -eq 1 ]
  [[ "$output" == *"[fail] deps"* ]]
  [ -d "$(env_root)" ]
  run "$SCRIPT" status
  [ "$status" -eq 1 ]
  [ "$(jq -r .result <<<"$output")" = unverified ]
}

@test "status: 検証の後に commit が増えると未検証に戻る" {
  commit_cmd_change b
  "$SCRIPT" run
  commit_cmd_change c
  run "$SCRIPT" status
  [ "$status" -eq 1 ]
  [ "$(jq -r .result <<<"$output")" = unverified ]
}

@test "status: commit が違っても tree が同じなら通る" {
  commit_cmd_change b
  "$SCRIPT" run
  git -C "$VERIFY_REPO" commit -q --allow-empty -m "merge の代わり"
  run "$SCRIPT" status
  [ "$status" -eq 0 ]
}

@test "status: ref を引数で受ける" {
  commit_cmd_change b
  "$SCRIPT" run
  commit_cmd_change c
  run "$SCRIPT" status HEAD~1
  [ "$status" -eq 0 ]
}

@test "status: 前の tag から gui に差分があれば、人の確認まで通さない" {
  printf 'b\n' >"$VERIFY_REPO/gui/main.go"
  git -C "$VERIFY_REPO" commit -q -am gui
  KEEP=1 "$SCRIPT" run
  run "$SCRIPT" status
  [ "$status" -eq 1 ]
  [ "$(jq -r .result <<<"$output")" = human_required ]
  "$SCRIPT" approve
  run "$SCRIPT" status
  [ "$status" -eq 0 ]
  [ "$(jq -r .result <<<"$output")" = pass ]
}

@test "status: 前の tag から mod に差分があれば、人の確認まで通さない" {
  printf 'b\n' >"$VERIFY_REPO/hooks/register.ts"
  git -C "$VERIFY_REPO" commit -q -am mod
  "$SCRIPT" run
  run "$SCRIPT" status
  [ "$status" -eq 1 ]
  [ "$(jq -r .result <<<"$output")" = human_required ]
}

@test "approve: 機械の点検が通っていなければ拒む" {
  commit_cmd_change b
  "$SCRIPT" up
  run "$SCRIPT" approve
  [ "$status" -ne 0 ]
}

@test "down: 環境を消す" {
  "$SCRIPT" up
  run "$SCRIPT" down
  [ "$status" -eq 0 ]
  [ ! -e "$(env_root)" ]
}

@test "down: 印の無いディレクトリは消さない" {
  root=$(env_root)
  mkdir -p "$root"
  printf 'keep\n' >"$root/precious"
  run "$SCRIPT" down
  [ "$status" -eq 2 ]
  [[ "$output" == *"印 (.agents-daemon-verify) が無い"* ]]
  [ -f "$root/precious" ]
}

@test "down: 環境の root が symlink なら消さない" {
  root=$(env_root)
  mkdir -p "$VERIFY_BASE" "$TEST_TMPDIR/elsewhere"
  printf '{}\n' >"$TEST_TMPDIR/elsewhere/.agents-daemon-verify"
  ln -s "$TEST_TMPDIR/elsewhere" "$root"
  run "$SCRIPT" down
  [ "$status" -eq 2 ]
  [[ "$output" == *"symlink"* ]]
  [ -f "$TEST_TMPDIR/elsewhere/.agents-daemon-verify" ]
}
