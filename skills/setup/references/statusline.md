# statusline へ state file の書き込みを足す

setup skill の 3 段目で `context/<session_id>.json` が無い､または古かったときに読む｡

statusline の仕事は stdin をそのまま plugin のバイナリへ渡すことだけ｡取り出しと state file の書式は
`agents-daemon ingest-statusline` が持つ (書式は
[architecture.md](../../agents-daemon/references/architecture.md) の「ingest-statusline が書く state」)｡

## statusline の設定の置き場

まず statusline がどこに設定されているかを見る｡
複数のファイルに書かれていれば､下のループで後に出たファイルの設定が効く｡

```sh
for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
  [ -f "$f" ] && jq -c --arg f "$f" 'select(.statusLine) | {file: $f, statusLine}' "$f"
done
```

- `statusLine` がどこにも無い: statusline のスクリプトを新しく作り､`statusLine` に登録する案を示す｡
  中身は stdin を `$input` に読んで下の 1 行を呼ぶだけでよい (表示は無くても daemon は動く)｡
- `statusLine.command` がスクリプトを指している: そのスクリプトを Read し､stdin の JSON を変数へ読んでいる箇所の後ろに
  下の 1 行を足す案を示す｡既に `ingest-statusline` を呼ぶ行があるのにファイルができていないなら､
  足さずに､その行が効いていない理由 (早期 return の後ろにある､バイナリがまだ無い､など) を調べる｡

どちらも差分を見せて確認を取ってから書き換える｡

## 足す 1 行

stdin の JSON が `$input` に入っている前提｡

```sh
# agents-daemon の daemon が読む state file｡描画を壊さないよう失敗は握りつぶす
"${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" ingest-statusline <<<"$input" 2>/dev/null || :
```

- 呼ぶのは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`｡SessionStart hook が建てるバイナリで､
  plugin の version をまたいでも置き場が変わらない｡plugin の中のスクリプト (`${CLAUDE_PLUGIN_ROOT}/...`) を
  statusline から呼ばせないのは､install 先が version ごとに変わり､更新した日から state file が黙って
  書かれなくなるため｡
- バイナリが無いとき (install 直後で SessionStart の build がまだ終わっていない) は `|| :` で黙る｡
  次の描画から書かれ始める｡
- `context/` は描画のたびに書き直され､`rate-limits/` は `rate_limits.five_hour.resets_at` が来たときだけ書かれる｡
  この出し分けはバイナリの側にあり､statusline は何も判断しない｡
- 1 回の実行は数 ms｡描画ごとに呼んでも表示は遅れない｡

足した後は statusline が 1 回描画されるのを待ってから (次の応答の後)､setup skill の 3 段目の確認をやり直す｡
