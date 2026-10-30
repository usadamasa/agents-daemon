# statusline が書く state

全体の連動の [1] (statusline が上限情報を渡し、バイナリが落とす) の詳細。全体の流れは
[architecture.md](architecture.md) の「全体の連動」。

## statusline の stdin と配線

<!-- cc-premise: 2.1.310 statusline stdin の rate_limits.five_hour / context_window と再実行の契機 -->
<!-- cc-premise: 2.1.310 statusline stdin の rate_limits はモデル単位の週次ウィンドウを含まない -->

Claude Code は statusline コマンドの stdin に JSON を渡す。その中に
`rate_limits.five_hour.{used_percentage, resets_at}` が入る (`resets_at` は Unix epoch 秒)。
この値は statusline の stdin にしか来ないので、statusline を経由するのは避けられない。
モデル単位の週次ウィンドウ (`/usage` の `Current week (Fable)` の行) は stdin に来ないので、
statusline には出せない (#116)。
配線は 2 通りで、取り出しはどちらもバイナリ (`sessionstate.Store.IngestStatusline`) が持つ
(`agents-daemon:setup` skill の `references/statusline.md`)。

- `statusLine.command` をバイナリの `statusline` に向ける (推奨)。描画 (`statusline` パッケージ) と state file の
  書き込みを同じプロセスで行う。月額上限の env (`CLAUDE_STATUSLINE_SPEND_CAP`) があれば cost の台帳
  (`statusline/spend`) にも記録する
- 利用者の statusline から stdin を `agents-daemon ingest-statusline` へ渡す 1 行を足す。state file だけを書く

statusline はイベントで再実行される (新しい assistant message、`/compact` の完了、permission mode の変更など。
`refreshInterval` を設定すればその間隔でも)。描画のたびではなく、idle のセッションでは走らない。

## state file の出し分け

- `rate_limits` は**初回 API レスポンス後**にだけ現れる。
- 無いときは何も書かない。既存ファイルも消さない (まだ届いていないセッションが、他のセッションの
  書いた state を壊さないため)。
- **5 時間ウィンドウが切り替わる瞬間は `five_hour` が来ない**。docs は `resets_at` を過ぎた window を
  落とすと書き、実機では `xx:10:01` のような境界時刻に null で観測した。どちらも書かず、最後に持っていた
  有効な state を残す。無い値で上書きすると Go 側は 0 に読むため、待機明けのゲートが「fresh な 0%」で送信を抑制し、
  15 分後に state が古くなって改めて検知しても `resets_at` が無くフォールバック
  (5 時間待ち) に落ちる。2026-08-28 の取りこぼしはこれが原因だった。daemon 側も
  `resets_at` が 0 の state は「値が無い」として扱う (`ratelimit.State.HasFiveHour`)。
  信号にするのは `resets_at` だけ。観測されたのは両方 null の形で、`used_percentage`
  だけが null の描画は見ていない。
- **セッションごとに別ファイルへ書く**。`resets_at` はアカウント全体で共通だが、
  使用率は観測した時点の値で、セッションによって観測時刻が違う。1 ファイルを共有すると、
  最後に statusline を描いたセッションの数字で別セッションの pane が判定される。実際、
  2026-08-19 の取りこぼしでは上限に達したセッションの pane が、無関係なセッション
  (次のウィンドウで動き出した) の使用率 6% でゲート抑制された。ゲートは自セッションの
  ファイルだけを見る。他セッションのファイルは起床時刻の算出にだけ使う
  ([rate-limit.md](rate-limit.md) の「待機時刻の決め方」参照)。
- 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う (`statefile`)。
- statusline の描画を壊さないよう fail-silent。失敗しても exit 0 で終わる (理由は stderr)。
  `ingest-statusline` は stdout に何も出さず、`statusline` は描画だけを出す。daemon のログにも書かない。

これがこのツールの中核。`resets_at` が epoch で直接手に入るので、画面の
`resets 12:30pm (Asia/Tokyo)` のような表記を am/pm やタイムゾーンごとパースする処理が要らない。
実機で `resets_at = 1786851000` と画面の `12:30pm (Asia/Tokyo)` が一致することを確認済み。

同じ呼び出しで `context/<session_id>.json` へも書く。
こちらは `rate_limits` と違って**statusline が走るたびに必ず書く**。
daemon が使うのは `multiplexer` (block から session_id への対応) で、同じ block の記録が複数あれば
`observed_at` が最新のものを採る。`session_name` は依存図のノードとメモに出すセッションの名前になる。
`used_percentage` は参考として写すだけで、daemon は読まない。

## 書式

`$STATE` は [architecture.md](architecture.md) の「出力されるファイル」と同じ
`${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`。
どちらも同一ディレクトリの一時ファイルへ書いてから rename で置き換える。
読み手 (daemon) と書き手 (`statusline` / `ingest-statusline`) は書式ごとのパッケージの同じ型を使うので、
書式はここに転記した参考で、実体は `rate-limits/` が `ratelimit`、`context/` が
`sessioncontext` にある。

`$STATE/rate-limits/<session_id>.json` (`rate_limits.five_hour.resets_at` があるときだけ):

```json
{
  "five_hour": {"used_percentage": 42, "resets_at": 1786851000},
  "seven_day": {"used_percentage": 18, "resets_at": 1787300000},
  "observed_at": 1786840000,
  "session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b"
}
```

`seven_day` は入力にあるときだけ付ける。`used_percentage` は整数に丸める。

`$STATE/context/<session_id>.json` (毎描画):

```json
{"session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b", "session_name": "ad-board-cards", "used_percentage": 63, "observed_at": 1786840000, "multiplexer": {"kind": "rex", "session": "session:01a1…", "block": "block:01a1…"}}
```

`observed_at` はどちらも書いた時点の Unix epoch 秒。

`session_name` は statusline の stdin の `session_name` をそのまま写し、入力に無ければ付けない。
<!-- cc-premise: 2.1.310 statusline session_name は custom title、無ければ AI の題 -->
中身は `--name` か `/rename` で付けた名前、無ければ Claude Code が会話から付けた題になる。`ListAgents` の名前とは
同じとは限らない (同じ名前のセッションが既にいると `ListAgents` 側だけ `workspace-dc` のような接尾辞が付く)。
依存図 (`deps`) はこの名前をノード、`repos` の `sessions`、メモの `sources` に出し、名前が無ければ
session ID の先頭 8 文字、同じ名前のセッションが複数あれば名前の後ろに session ID の先頭を足す。記録は `context/` の
保持期間 (7 日) の間残るので、終了したセッションのノードとメモにも名前が付く。

`multiplexer` は Claude Code が動いている多重化ツールの場所。statusline のコマンドは Claude Code の環境を
継承するので、バイナリが環境変数から読んで書く (`sessioncontext.MultiplexerRefFromEnv`)。

| 環境変数 | 書く値 |
| ---- | ---- |
| `REX_BLOCK` がある | `{"kind": "rex", "session": $REX_SESSION, "block": $REX_BLOCK}` |
| 無い | フィールドを付けない |

backend は block から session_id をこの記録で逆引きする (`sessioncontext.Store.ListMultiplexerBlocks`)。同じ ID の
記録が複数あれば `observed_at` が最新のものを採る (同じ block で Claude Code を起動し直すと古い記録が残る)。
`context/` は 7 日で消えるだけなので、5 時間の上限待ちを跨いでも対応は残る。制約 (初回描画までの
数秒) は [operations.md](../../../../skills/agents-daemon/references/operations.md) の「既知の制約」。

skill がセッションの中から自分の session_id を引くときは `CLAUDE_CODE_SESSION_ID` を唯一の情報源にする
(`scripts/get-session-id.sh`)。
