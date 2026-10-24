#!/bin/bash
set -euo pipefail
# fuzz.sh
# go の fuzz target (Fuzz*) を全部、1 つずつ FUZZTIME (既定 30s) だけ回す。`task fuzz` が呼ぶ。
# `go test -fuzz` は 1 回の起動で 1 つの target しか受けないので、`go test -list` で集めて順に回す。
# 反例は testdata/fuzz/<Fuzz 名>/ に落ち、以後の `go test` が seed として回す。
# 本物のバグを示すものだけを commit する。
#
# 乱択で探すのはここだけで、`task test` は seed だけを回す (CI の時間を固定するため)。

fuzztime="${FUZZTIME:-30s}"
cd "$(dirname "$0")/.."

# `go test -list` は package ごとに target 名の行を出した後に `ok <pkg> <time>` の行を出す。
# target 名を貯めて、`ok` の行が来たらその package のぶんを回す。
pending=()
failed=0
while IFS= read -r line; do
  case "$line" in
    Fuzz*)
      pending+=("$line")
      ;;
    ok*)
      pkg=$(printf '%s\n' "$line" | awk '{print $2}')
      if [ "${#pending[@]}" -gt 0 ]; then
        for target in "${pending[@]}"; do
          printf '==> %s %s (%s)\n' "$pkg" "$target" "$fuzztime"
          if ! go test -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime" "$pkg"; then
            failed=1
          fi
        done
      fi
      pending=()
      ;;
  esac
done < <(go test -list '^Fuzz' ./...)

exit "$failed"
