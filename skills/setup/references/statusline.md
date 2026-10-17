# statusline の配線

setup skill の 3 段目で `context/<session_id>.json` が無い､または古かったときに読む｡

使用率と解除時刻は statusline の stdin にしか来ない｡配線は 2 通りあり､どちらも
取り出しと state file の書式はバイナリ (`internal/sessionstate`) が持つ (書式は
[architecture.md](../../agents-daemon/references/architecture.md) の「statusline が書く state」)｡

| 配線 | 向く人 |
| ---- | ---- |
| A. `statusLine.command` をバイナリの `statusline` に向ける (推奨) | 自前の statusline を持たない､または plugin の描画 (3 行｡model / 使用量 / 居場所) でよい |
| B. 自前の statusline から `ingest-statusline` へ stdin を渡す 1 行を足す | 自前の描画を残したい |

## statusline の設定の置き場

まず statusline がどこに設定されているかを見る｡
複数のファイルに書かれていれば､下のループで後に出たファイルの設定が効く｡

```sh
for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
  [ -f "$f" ] && jq -c --arg f "$f" 'select(.statusLine) | {file: $f, statusLine}' "$f"
done
```

- `statusLine` がどこにも無い: A を示す｡
- `statusLine.command` がスクリプトを指している: 利用者に A と B のどちらにするかを聞く｡
  B なら そのスクリプトを Read し､stdin の JSON を変数へ読んでいる箇所の後ろに下の 1 行を足す案を示す｡
  既に `ingest-statusline` を呼ぶ行があるのにファイルができていないなら､足さずに､その行が効いていない理由
  (早期 return の後ろにある､バイナリがまだ無い､など) を調べる｡

どちらも差分を見せて確認を取ってから書き換える｡

## A. `statusLine.command` をバイナリに向ける

settings.json の `statusLine` を次にする｡

```json
"statusLine": {
  "type": "command",
  "command": "${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon statusline",
  "padding": 0
}
```

- 呼ぶのは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`｡SessionStart hook が建てるバイナリで､
  plugin の version をまたいでも置き場が変わらない｡plugin の中のパス (`${CLAUDE_PLUGIN_ROOT}/...`) を
  statusline から呼ばせないのは､install 先が version ごとに変わり､更新した日から黙って動かなくなるため｡
- 描画の前に同じプロセスで state file (`context/` `rate-limits/`) を書く｡描画ごとの fork は 1 つで済む｡
- バイナリが無いとき (install 直後で SessionStart の build がまだ終わっていない) は command が失敗し､
  その間 statusline は何も出ない｡build が終われば次の描画から出る｡B は `|| :` で描画を守るので､
  この空白が無い｡
- 月額上限に対する消費 (📅 / 🧾) を出すには環境変数 `CLAUDE_STATUSLINE_SPEND_CAP` (USD の整数) を
  settings の `env` に置く｡rate_limits が届かない契約 (Enterprise の seat) 向けで､届くプロファイル
  (⏳ が出る) では設定しない｡
- 幅で出力量が変わる (100 桁未満は 1 行､60 桁未満は何も出さない)｡何も出ないときは pane の幅を見る｡

## B. 自前の statusline に 1 行足す

stdin の JSON が `$input` に入っている前提｡

```sh
# agents-daemon の daemon が読む state file｡描画を壊さないよう失敗は握りつぶす
"${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" ingest-statusline <<<"$input" 2>/dev/null || :
```

- バイナリが無いときは `|| :` で黙る｡次の描画から書かれ始める｡
- `context/` は描画のたびに書き直され､`rate-limits/` は `rate_limits.five_hour.resets_at` が来たときだけ書かれる｡
  この出し分けはバイナリの側にあり､statusline は何も判断しない｡
- 1 回の実行は数 ms｡描画ごとに呼んでも表示は遅れない｡

## 足した後

statusline が 1 回描画されるのを待ってから (次の応答の後)､setup skill の 3 段目の確認をやり直す｡
