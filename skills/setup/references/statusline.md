# statusline へ state file の書き込みを足す

setup skill の 3 段目で `context/<session_id>.json` が無い､または古かったときに読む｡

## statusline の設定の置き場

まず statusline がどこに設定されているかを見る｡
複数のファイルに書かれていれば､下のループで後に出たファイルの設定が効く｡

```sh
for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
  [ -f "$f" ] && jq -c --arg f "$f" 'select(.statusLine) | {file: $f, statusLine}' "$f"
done
```

- `statusLine` がどこにも無い: statusline のスクリプトを新しく作り､`statusLine` に登録する案を示す｡
- `statusLine.command` がスクリプトを指している: そのスクリプトを Read し､stdin の JSON を変数へ読んでいる箇所の後ろに
  下のスニペットを足す案を示す｡既に `agents-daemon` の state を書く処理があるのにファイルができていないなら､
  足さずに､その処理が効いていない理由 (早期 return の後ろにある､書き込み先が違う､など) を調べる｡

どちらも差分を見せて確認を取ってから書き換える｡

## スニペット

stdin の JSON が `$input` に入っている前提｡state file の書式は
[architecture.md](../../agents-daemon/references/architecture.md) の「statusline が書く state」に従う｡

```sh
# agents-daemon の daemon が読む state file｡描画を壊さないよう失敗は握りつぶす
ad_state="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"
ad_sid=$(jq -r '.session_id // empty' <<<"$input" 2>/dev/null)
if [ -n "$ad_sid" ]; then
  {
    ad_dir="$ad_state/context"
    mkdir -p "$ad_dir" &&
      ad_tmp=$(mktemp "$ad_dir/.context.XXXXXX") &&
      jq -c '{session_id, used_percentage: ((.context_window.used_percentage // 0) | round), observed_at: (now | floor)}' \
        <<<"$input" >"$ad_tmp" &&
      mv "$ad_tmp" "$ad_dir/$ad_sid.json"
  } 2>/dev/null || :
  # five_hour が null の描画 (初回・ウィンドウの切り替わり) では書かず､既存ファイルも残す
  if jq -e '.rate_limits.five_hour.resets_at // empty' <<<"$input" >/dev/null 2>&1; then
    {
      ad_dir="$ad_state/rate-limits"
      mkdir -p "$ad_dir" &&
        ad_tmp=$(mktemp "$ad_dir/.rate-limits.XXXXXX") &&
        jq -c '.rate_limits as $r
          | {five_hour: {used_percentage: ($r.five_hour.used_percentage // 0 | round), resets_at: $r.five_hour.resets_at}}
          + (if $r.seven_day.resets_at then {seven_day: {used_percentage: ($r.seven_day.used_percentage // 0 | round), resets_at: $r.seven_day.resets_at}} else {} end)
          + {observed_at: (now | floor), session_id}' \
          <<<"$input" >"$ad_tmp" &&
        mv "$ad_tmp" "$ad_dir/$ad_sid.json"
    } 2>/dev/null || :
  fi
fi
```

- `context/` はスニペットが描画のたびに書き直す｡daemon は `observed_at` の新しさでセッションが生きているかも見るので､
  `rate-limits/` と違って条件を付けずに毎回書く｡
- statusline から plugin の中のスクリプト (`${CLAUDE_PLUGIN_ROOT}/...`) を呼ばせない｡plugin の install 先は version ごとに変わるので､
  plugin を更新した日から state file が黙って書かれなくなる｡スニペットは statusline 側へ直接置く｡
- 一時ファイルは書き込み先と同じディレクトリに作る｡別のファイルシステムだと `mv` が原子的でなくなり､daemon が書きかけを読む｡

足した後は statusline が 1 回描画されるのを待ってから (次の応答の後)､setup skill の 3 段目の確認をやり直す｡
