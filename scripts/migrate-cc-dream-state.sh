#!/bin/bash
# migrate-cc-dream-state.sh
# 旧 cc-dream の state (${XDG_STATE_HOME:-~/.local/state}/cc-dream) を agents-daemon/dream/ へ移す、一回きりの移行スクリプト。
#
# バイナリと hook は旧パスを読まない。移行しなくても次の harvest が直近 14 日分を作り直すので、
# 移すのは harvest の履歴と candidates / replays / verdicts (判断の材料) であって、正しさではない。
#
# - 中身を黙って混ぜない: 移す先に同じ名前があれば何も動かさずに失敗する
# - 知らないエントリが旧ディレクトリにあれば失敗する (消してよいものか人が見る)
# - harvest.log (旧 Stop hook の stderr) は捨てる。新しいログは agents-daemon/logs/dream-harvest.log
#
# 移行が済んだらこのスクリプトとテスト (tests/migrate-cc-dream-state.bats) は消す。

set -euo pipefail

state_home="${XDG_STATE_HOME:-$HOME/.local/state}"
old="$state_home/cc-dream"
new="$state_home/agents-daemon/dream"

if [ ! -d "$old" ]; then
  printf '%s は無いので移行するものはありません\n' "$old"
  exit 0
fi

# 旧 cc-dream が書いていたもの。これ以外が残っていたら手で見る。
move_entries=(harvest harvest-state.json candidates replays verdicts)
drop_entries=(harvest.log)

is_known() {
  local name="$1" e
  for e in "${move_entries[@]}" "${drop_entries[@]}"; do
    [ "$name" = "$e" ] && return 0
  done
  return 1
}

# 動かす前に全部を検査する。1 つでも駄目なら何も動かさない。
status=0
for path in "$old"/* "$old"/.[!.]*; do
  [ -e "$path" ] || continue
  name=$(basename "$path")
  if ! is_known "$name"; then
    printf 'ERROR: %s は cc-dream の state として知らない名前です。中身を見て手で消すか移してください\n' "$path" >&2
    status=1
  fi
done
for name in "${move_entries[@]}"; do
  if [ -e "$old/$name" ] && [ -e "$new/$name" ]; then
    printf 'ERROR: 移す先 %s が既にあります。中身を混ぜないので、どちらを残すか決めて片方を消してください\n' "$new/$name" >&2
    status=1
  fi
done
[ "$status" -eq 0 ] || exit 1

if ! mkdir -p "$new"; then
  printf 'ERROR: mkdir failed: %s\n' "$new" >&2
  exit 1
fi
for name in "${move_entries[@]}"; do
  [ -e "$old/$name" ] || continue
  if ! mv "$old/$name" "$new/$name"; then
    printf 'ERROR: mv failed: %s -> %s\n' "$old/$name" "$new/$name" >&2
    exit 1
  fi
  printf 'moved %s -> %s\n' "$old/$name" "$new/$name"
done
for name in "${drop_entries[@]}"; do
  [ -e "$old/$name" ] || continue
  if ! rm -f "$old/$name"; then
    printf 'ERROR: rm failed: %s\n' "$old/$name" >&2
    exit 1
  fi
  printf 'dropped %s\n' "$old/$name"
done
if ! rmdir "$old"; then
  printf 'ERROR: rmdir failed: %s (まだ何か残っています)\n' "$old" >&2
  exit 1
fi
printf 'done: %s -> %s\n' "$old" "$new"
