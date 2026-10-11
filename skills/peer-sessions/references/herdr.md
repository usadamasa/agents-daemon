# herdr のペイン操作

herdr の CLI 構文の正は herdr 自身が出す help で、コマンド群を引数なしで叩くと一覧が出る
(`herdr pane`、`herdr agent`)。`herdr --skill` が同じ内容を skill の形で出す。ここでは、その通りに
叩いても期待どおりに動かない箇所と、peer セッションを立てるときの手順だけを扱う。

`herdr agent list` が JSON を返さず `Os { code: 1, kind: PermissionDenied }` を返すなら、
CLI の使い方ではなく sandbox が unix socket を塞いでいる。ハーネス側の設定の問題なので、
`agents-daemon:setup` skill の「多重化ツールの到達性」で切り分ける。

`herdr --skill` の生成物を skill として入れている環境では、その description にある
「herdr と明示されない限り使うな」は、背景ターミナルがあれば便利という程度の状況を弾くための条件で、
peer を立てる依頼は該当しない。

## help に書かれていない前提

- ID は `w1` (workspace)、`w1:t1` (tab)、`w1:p1` (pane) の形で、閉じた ID は再利用されない。
  別 workspace へ `pane move` した pane は新しい ID を受け取る。続きは応答の
  `.result.move_result.pane.pane_id` か agent 名で指す
- agent の状態は `idle` / `done` / `blocked` / `unknown` / `working`。`idle` と `done` はどちらも入力待ちで、
  違いはその tab が herdr の UI で見られたかどうか。`unknown` は agent は居るが分類できない状態で、
  完了の証拠にはならない
- `agent start` は既存の shell pane (対話プロンプトで待っている pane) にしか起動できず、pane を作らない。
  先に `pane split` で器を作る
- 自分で作っていない workspace / tab / pane は閉じない。`herdr server stop` は自分のセッションも巻き込む

## peer の Claude セッションを立てる

```sh
herdr pane split --pane "$HERDR_PANE_ID" --direction right --cwd "$PWD" --no-focus
herdr agent start peer-b --kind claude --pane <returned-pane-id> \
  -- --name peer-b --model "${CLAUDE_CODE_SUBAGENT_MODEL:?}"
```

- pane ID は split のレスポンスの `.result.pane.pane_id` から読む
- 渡す先が別リポジトリなら `--cwd` をそのパスにする
- **`--` の後ろに `--name <name>` を渡す**。`--` の後ろのフラグは herdr が claude の argv へ
  そのまま渡す (`agent start` のレスポンスの `argv` で確認できる)

`--kind claude` だけだと argv が `["claude"]` になり、通常の REPL ではなく FleetView
(agents 画面) が開く。FleetView へ投げたプロンプトは対話ターンにならず background job として
実行され、投入後も `idle` のままなので `--wait` は必ず `agent_prompt_stalled` を返す。
`--name` を渡せばこの状態にならない。

### 1 エージェント = 1 タブにする

`herdr pane move <pane-id> --new-tab` で分け、`herdr tab rename` にエージェント名を付ける。

### 畳む

`herdr pane close <pane-id>` でペインごと畳む (`herdr agent stop` は無い)。

## background job から叩くとき

`$HERDR_PANE_ID` は既に閉じた pane を指す。env を継承するためで、`--current` は
`pane_not_found` (exit 1) になる。`herdr pane list` で生存を確認してから pane ID を明示する。

エラー時の exit code は 1。ただしパイプに繋ぐと `$?` は後段のものになるので
`${PIPESTATUS[0]}` を見る。

## 送ったのに相手が動き出さないとき

`--name` を渡して正しく起動した対話セッションでも、相手のペインに `SendMessage` の本文は
表示されるのに、`idle` のまま止まってターンが始まらないことがある。`SendMessage` 自体は
`success` を返すので、送信側からは成功に見える。

この状態では `herdr agent prompt` も効かず `agent_prompt_stalled` を返す (`state_change_seq` が
動かない)。上の FleetView 起因のものと同じエラー名だが原因が違う。`--name` は付いているので、
起動し直しても直らない。

まず、次のコマンドでプロセスの生死を分ける。

```sh
herdr pane process-info --pane <pane-id>
```

`claude --name <name>` が `foreground_processes` に居れば生きている。入力待ちで止まっている
だけなので、次のようにペインへ直接キーを送って起こす。

```sh
herdr pane send-text <pane-id> "<続きの指示>"
herdr pane send-keys <pane-id> Enter
```

`send-text` は Enter を含まない。`send-keys ... Enter` まで送って初めて投入される。
数秒後に `herdr agent list` で状態が `working` へ変われば復帰している。

送り直す本文は短くてよい。元の `SendMessage` の内容は相手の画面に残っており、コンテキスト
としては届いている。

## pane ID の渡し方がサブコマンドごとに違う

`unknown option: <pane-id>` や `usage:` が出たら、もう一方の形を試す。確実なのは
`herdr pane <サブコマンド> --help` の Usage 行を読むことで、`herdr pane --help` の一覧からは
区別が付かない。

| 形 | サブコマンド |
| ---- | ---- |
| 位置引数のみ | `get` `read` `run` `close` `move` `rename` `send-text` `send-keys` `wait-output` |
| `--pane` / `--current` のみ | `list` `current` `layout` `process-info` `edges` `neighbor` `focus` `resize` `swap` |
| どちらでも通る | `split` `zoom` |
