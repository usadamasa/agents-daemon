#!/usr/bin/env bats
# PR が閉じる issue の種類と area のラベルを PR へ写す script を検証する。
#
# release note の category は PR のラベルで決まるので、写し漏れも写し過ぎ (epic、priority) も note に出る。

bats_require_minimum_version 1.5.0

setup() {
  load lib/hook-test-helpers
  SCRIPT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)/scripts/sync-pr-labels.sh"
  export TEST_TMPDIR="$BATS_TEST_TMPDIR"
  mkdir -p "$TEST_TMPDIR/bin"
  cp "$BATS_TEST_DIRNAME/fixtures/fake-gh" "$TEST_TMPDIR/bin/gh"
  chmod +x "$TEST_TMPDIR/bin/gh"
  export PATH="$TEST_TMPDIR/bin:$PATH"
}

readonly REPO_URL="https://github.com/usadamasa/agents-daemon"

# pr_json <PR の既存ラベル (JSON 配列)> <closing issue の URL...>
pr_json() {
  local labels="$1"
  shift
  jq -n --arg url "$REPO_URL/pull/10" --argjson labels "$labels" \
    '{url: $url, labels: ($labels | map({name: .})), closingIssuesReferences: ($ARGS.positional | map({url: .}))}' \
    --args "$@" >"$TEST_TMPDIR/pr.json"
}

# issue_json <番号> <ラベル (JSON 配列)>
issue_json() {
  jq -n --argjson labels "$2" '{labels: ($labels | map({name: .}))}' >"$TEST_TMPDIR/issue-$1.json"
}

edit_calls() {
  grep '^pr edit' "$TEST_TMPDIR/gh.calls" || true
}

@test "PR の番号が無ければ usage を出して exit 1 する" {
  run_hook --separate-stderr "$SCRIPT"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"usage"* ]]
}

@test "PR の番号が数字でなければ exit 1 する" {
  run_hook --separate-stderr "$SCRIPT" "10; rm"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"usage"* ]]
}

@test "closing issue の種類と area を写し、epic と priority は写さない" {
  pr_json '[]' "$REPO_URL/issues/1"
  issue_json 1 '["bug", "area: rex", "epic", "priority: high", "duplicate"]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -eq 0 ]
  [ "$(edit_calls)" = "pr edit 10 --add-label area: rex,bug" ]
}

@test "複数の issue に同じラベルがあっても 1 回だけ足す" {
  pr_json '[]' "$REPO_URL/issues/1" "$REPO_URL/issues/2"
  issue_json 1 '["enhancement", "area: deps-graph"]'
  issue_json 2 '["enhancement", "area: handoff"]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -eq 0 ]
  [ "$(edit_calls)" = "pr edit 10 --add-label area: deps-graph,area: handoff,enhancement" ]
}

@test "PR に付いているラベルは足さない" {
  pr_json '["area: rex"]' "$REPO_URL/issues/1"
  issue_json 1 '["refactor", "area: rex"]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -eq 0 ]
  [ "$(edit_calls)" = "pr edit 10 --add-label refactor" ]
}

@test "足すラベルが無ければ pr edit を呼ばない" {
  pr_json '["documentation", "area: dev"]' "$REPO_URL/issues/1"
  issue_json 1 '["documentation", "area: dev"]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -eq 0 ]
  [ -z "$(edit_calls)" ]
}

@test "closing issue が無ければ pr edit を呼ばない" {
  pr_json '[]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -eq 0 ]
  [ -z "$(edit_calls)" ]
}

@test "別の repo の issue はラベルを読まない" {
  pr_json '[]' "https://github.com/usadamasa/other/issues/1" "$REPO_URL/issues/2"
  issue_json 1 '["bug"]'
  issue_json 2 '["investigation"]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -eq 0 ]
  ! grep -q 'other/issues/1' "$TEST_TMPDIR/gh.calls"
  [ "$(edit_calls)" = "pr edit 10 --add-label investigation" ]
}

@test "issue を読めなければ非 0 で終わり、pr edit を呼ばない" {
  pr_json '[]' "$REPO_URL/issues/1" "$REPO_URL/issues/2"
  issue_json 1 '["bug"]'
  run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -ne 0 ]
  [[ "$stderr" == *"issue 2 not found"* ]]
  [ -z "$(edit_calls)" ]
}

@test "pr edit が失敗したら非 0 で終わる" {
  pr_json '[]' "$REPO_URL/issues/1"
  issue_json 1 '["bug"]'
  GH_STUB_EDIT_EXIT=1 run_hook --separate-stderr "$SCRIPT" 10
  [ "$status" -ne 0 ]
}
