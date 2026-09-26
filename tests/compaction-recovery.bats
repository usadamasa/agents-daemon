#!/usr/bin/env bats
# PostCompact の marker 書き出しと、UserPromptSubmit の復旧ガイド注入の 2 段構成を検証する｡
#
# この 2 本が繋がらないと、daemon が compact を投入しても復旧材料が会話へ届かない｡
# marker と state file の置き場は compact-markers.sh が XDG state 配下へ解決するため、
# テストは XDG_STATE_HOME を一時ディレクトリへ差し替える｡

setup() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  POST_HOOK="$REPO_ROOT/hooks/compaction-recovery.sh"
  PROMPT_HOOK="$REPO_ROOT/hooks/userpromptsubmit-compaction-recovery.sh"

  TEST_TMPDIR=$(mktemp -d "${TMPDIR:-/tmp}/agents-daemon-test.XXXXXX")
  export XDG_STATE_HOME="$TEST_TMPDIR/state"
  BASE="$XDG_STATE_HOME/agents-daemon"
  # hook-logger の出力先を一時ディレクトリへ寄せる (実 HOME を汚さない)｡
  export HOME="$TEST_TMPDIR/home"
  mkdir -p "$HOME/.claude"
}

teardown() {
  [ -n "${TEST_TMPDIR:-}" ] && rm -rf "$TEST_TMPDIR"
}

# ヘルパー: session_id だけを持つ hook 入力 JSON
input_for() {
  jq -n --arg s "$1" '{session_id: $s}'
}

@test "PostCompact hook は XDG state 配下へ marker を書く" {
  run bash "$POST_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]
  [ -f "$BASE/compacted/sess-1" ]
}

@test "PostCompact hook は session_id が空なら何もしない" {
  run bash "$POST_HOOK" <<<'{}'
  [ "$status" -eq 0 ]
  [ ! -d "$BASE/compacted" ]
}

@test "marker があれば復旧ガイドを注入し marker を消す" {
  mkdir -p "$BASE/compacted"
  touch "$BASE/compacted/sess-1"

  run bash "$PROMPT_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]

  # additionalContext に復旧ガイドが入っている
  run jq -er '.hookSpecificOutput.additionalContext' <<<"$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"[compact-recovery]"* ]]

  # one-shot: 消えている
  [ ! -f "$BASE/compacted/sess-1" ]
}

@test "state file があればそのパスをガイドに載せる" {
  mkdir -p "$BASE/compacted" "$BASE/compact-state"
  touch "$BASE/compacted/sess-1"
  printf '# Compact Prep State\n' > "$BASE/compact-state/sess-1.md"

  run bash "$PROMPT_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]

  run jq -er '.hookSpecificOutput.additionalContext' <<<"$output"
  [[ "$output" == *"$BASE/compact-state/sess-1.md"* ]]
  [[ "$output" == *"**あり**"* ]]
}

@test "state file が無ければ無い旨をガイドに載せる" {
  mkdir -p "$BASE/compacted"
  touch "$BASE/compacted/sess-1"

  run bash "$PROMPT_HOOK" <<<"$(input_for sess-1)"
  run jq -er '.hookSpecificOutput.additionalContext' <<<"$output"
  [[ "$output" == *"**なし**"* ]]
}

@test "marker が無ければ何も出さない" {
  run bash "$PROMPT_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "別セッションの marker では発火しない" {
  mkdir -p "$BASE/compacted"
  touch "$BASE/compacted/other"

  run bash "$PROMPT_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ -f "$BASE/compacted/other" ]
}

@test "PostCompact から UserPromptSubmit まで通しで繋がる" {
  # daemon が /compact を投入した後に起きる経路をそのまま辿る｡
  printf '# Compact Prep State\n' > "$TEST_TMPDIR/state-body.md"
  mkdir -p "$BASE/compact-state"
  cp "$TEST_TMPDIR/state-body.md" "$BASE/compact-state/sess-1.md"

  run bash "$POST_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]
  [ -f "$BASE/compacted/sess-1" ]

  run bash "$PROMPT_HOOK" <<<"$(input_for sess-1)"
  [ "$status" -eq 0 ]
  run jq -er '.hookSpecificOutput.additionalContext' <<<"$output"
  [[ "$output" == *"$BASE/compact-state/sess-1.md"* ]]
  [ ! -f "$BASE/compacted/sess-1" ]
}
