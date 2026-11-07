#!/usr/bin/env bats
# gui/ (依存図のアプリ) のテストが gui/ の外のソースに依らないことを確かめる。
#
# CI は gui/ と .github/filters.yaml の gui に挙げたパスを変えた PR でだけ gui test を回す。gui/ の外を変えた PR で
# gui test を飛ばしてよい前提がこの性質で、ここが崩れたら filters.yaml の gui にそのパスを足す。

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
}

@test "gui の module は root の module を require も replace もしない" {
  local mod
  mod="$(go mod edit -json "$ROOT/gui/go.mod")"
  run jq -nr --argjson m "$mod" \
    '[($m.Require // [])[].Path | select(startswith("github.com/usadamasa/agents-daemon"))] + [($m.Replace // [])[].Old.Path] | .[]'
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "gui のフロントエンドは gui/frontend の外を import しない" {
  run grep -rEn --include='*.js' --exclude-dir=node_modules "(from|import\()[[:space:]]*['\"](\.\./){2,}" "$ROOT/gui/frontend"
  [ "$status" -eq 1 ]
}
