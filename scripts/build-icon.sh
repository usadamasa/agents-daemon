#!/bin/bash
set -euo pipefail
# build-icon.sh
# 依存図のアプリ (agent-tasks) のアイコンの原本 gui/darwin/agent-tasks.svg から gui/darwin/agent-tasks.icns と
# plugin の listing 用アイコン .claude-plugin/icon.png (1024px の PNG、SVG は受け付けられない) を作り直す。
# `task gui:icon` が呼ぶ。SVG を変えたら打ち、生成物もコミットする。
# build-gui.sh は生成済みの .icns を写すだけにして、SessionStart の build の前提に sips の SVG 描画 (macOS の
# バージョンで変わりうる) を足さない。

cd "$(dirname "$0")/.."
readonly src=gui/darwin/agent-tasks.svg
readonly dst=gui/darwin/agent-tasks.icns
readonly plugin_icon=.claude-plugin/icon.png

if [ "$(uname -s)" != Darwin ]; then
  printf 'build-icon.sh は macOS の sips と iconutil を使う\n' >&2
  exit 1
fi
if [ ! -f "$src" ]; then
  printf '%s が無い\n' "$src" >&2
  exit 1
fi

work=$(mktemp -d)
readonly work
trap 'rm -rf "$work"' EXIT

# 1024 の PNG を 1 枚描き、iconutil が読む名前 (icon_<pt>x<pt>[@2x].png) の各サイズへ縮める。
readonly master="$work/master.png"
sips -s format png "$src" --out "$master" >/dev/null
readonly iconset="$work/agent-tasks.iconset"
mkdir "$iconset"
for pt in 16 32 128 256 512; do
  sips -z "$pt" "$pt" "$master" --out "$iconset/icon_${pt}x${pt}.png" >/dev/null
  px=$((pt * 2))
  sips -z "$px" "$px" "$master" --out "$iconset/icon_${pt}x${pt}@2x.png" >/dev/null
done

iconutil -c icns "$iconset" -o "$dst"
printf '%s (%s bytes)\n' "$dst" "$(wc -c <"$dst" | tr -d ' ')"

cp "$master" "$plugin_icon"
printf '%s (%s bytes)\n' "$plugin_icon" "$(wc -c <"$plugin_icon" | tr -d ' ')"
