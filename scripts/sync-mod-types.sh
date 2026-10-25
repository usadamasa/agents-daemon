#!/bin/bash
set -euo pipefail
# sync-mod-types.sh
# Claude Code が .claude-plugin/types/ に書いた mod の型を ci/mod-types/ へ写す。`task mod-types:sync` が呼ぶ。
# 型は対話セッションが mod を hot-reload したときにしか書かれず、CI では用意できないので、写しをコミットして
# `task typecheck:ci` がそれを読む。claude-code-mcp は接続中の MCP の一覧で人ごとに違うので写さない。
# GitHub の anthropics/claude-code の mods/types/claude-code.d.ts は版が古く、この機械の built-in tools の表も無いので使わない。
# cc-premise: 2.1.310 mods 型は対話の hot-reload でだけ書かれ、claude plugin test と claude -p --plugin-dir では書かれない

cd "$(dirname "$0")/.."
src=.claude-plugin/types
dst=ci/mod-types

for pkg in claude-code claude-code-tools; do
  if [[ ! -f "$src/$pkg/index.d.ts" ]]; then
    printf '%s が無い。対話セッションで mod を読み込んで (claude --plugin-dir .) から打ち直す\n' "$src/$pkg/index.d.ts" >&2
    exit 1
  fi
done

for pkg in claude-code claude-code-tools; do
  mkdir -p "$dst/$pkg"
  cp "$src/$pkg/index.d.ts" "$dst/$pkg/index.d.ts"
done

printf '%s\n' "$(head -n 1 "$dst/claude-code/index.d.ts")"
