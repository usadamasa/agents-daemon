# rex の block / window 操作

rex はターミナルマルチプレクサで、`rex` CLI が unix socket 経由でサーバーを操作する。
構文の正は `rex <command> -h`。ここには help に書かれていない実測の挙動だけを置く。

自分の位置は `rex whoami --json` で取る。`.session_id` `.window_id` `.block_id` が自分の
session / window / block で、`.endpoint` が socket のパス。

## 用語と ID

| 単位 | 説明 | 作るコマンド |
| ---- | ---- | ---- |
| session | 1 つの作業空間。`rex ls` で一覧 | `rex new` |
| window | session 内のタブ。1 つ以上の block を持つ | `rex run`、`rex window new` |
| block | 1 つのターミナル (ペイン)。`REX_BLOCK` が自分 | `rex split`、`rex block new` |

ターゲットは完全 ID (`block:01a1…`)、`rex block ls` が出す末尾 6 桁、`--label` で付けた名前の
どれでも通る。作ったブロックには `--label` を付け、以後はラベルで指す。

## 作法

- 背景の作業は `--focus=false` を付ける。`run` も `split` も既定ではフォーカスを奪う。
- `send` `send-key` `capture` `wait` `block call` には `-b <target>` を明示する。省くと「フォーカス中の
  ブロック」になり、それはユーザーが見ているブロックかもしれない。`capture` と `zoom` と `block call` は
  `REX_BLOCK` を既定にするが、`send` はそうとは書かれていない。
- `--json` を付けて ID を結果から読む。`split` と `run` は `block_ids[0]` に新しいブロックを返す。
- `split` は同じ window に並べ、`run` は新しい window を作る。別のエージェントを立てるときは `run`
  (1 エージェント = 1 window)。
- コマンドはログインシェルの環境で動く。PATH は効くが alias は効かない。

## peer の Claude セッションを立てる

```sh
rex run --focus=false --cwd "$PWD" --label peer-b --json -- \
  claude --name peer-b --model "${CLAUDE_CODE_SUBAGENT_MODEL:?}"
```

数秒後に `ListAgents` へ `peer-b` として現れる。rex には herdr の `agent prompt` に当たるものは
無く、画面へ直接投入するなら `send -b peer-b '<本文>'` と `send-key -b peer-b enter` を使う。

### 畳む

`rex window close <window>` で window ごと畳む。`run --json` が返した `window_id` を使う。

## 別ブロックでコマンドを走らせて結果を読む

コマンドが終わるとブロックは自動で閉じ、`capture` は `block target "<label>" not found` になる。
出力を読みたいなら `--keep-open` を付ける。

```sh
rex split --focus=false --cwd "$PWD" --keep-open --label probe --json -- go test ./...
rex wait --json probe          # {"exit_code":1,"error":"exit status 1"} が返り、exit code も子に揃う
rex capture -b probe --trim --unwrap
rex block close probe
```

`wait` は子プロセスの終了を待つ。`--for 10m` で上限を切れる。長く掛かる `wait` は最初から
`run_in_background` で呼ぶ。

既存の shell ブロックで動かすなら `send` と `send-key enter` を分けて送る。`send` は Enter を含まない。

```sh
rex send -b work 'task test'
rex send-key -b work enter
rex send-key -b work ctrl+c     # 中断
```

### 画面の読み方

`capture --trim` は末尾の空行を落とし、`--unwrap` はソフトラップを繋ぐ。プロンプトが折り返されて
ブランチ名が途中で切れていたら `--unwrap` が要る。`--format html` は装飾を残す。

### プロセスの確認

```sh
rex block call -b work process
```

`.foreground.argv0` が前景のコマンド名。`.foreground.name` はバイナリ名で、claude の場合は
バージョン文字列 (`2.1.296` のような値) になるので、`name` で `claude` を探さない。

## やらないこと

- `rex kill` `rex server stop`: 自分のセッションも巻き込む。ユーザーが明示したときだけ
- 自分が作っていない block や window を閉じる

## 症状と対処

| 症状 | 対処 |
| ---- | ---- |
| `connect: operation not permitted` | sandbox が socket を塞いでいる。ハーネス側の設定の問題で CLI の問題ではない。`agents-daemon:setup` skill の「多重化ツールの到達性」 |
| `block target "<label>" not found` | コマンドが終わってブロックが閉じた。`--keep-open` を付けて作り直す |
| `window target "<id>" not found` | ID の桁を間違えている。`rex window ls` の ID 列をそのまま使う |
| `send` したのに動かない | `send-key -b <target> enter` を送っていない |
| `wait` が Usage を出して終わる | 子が非 0 で終わっただけ。exit code と `--json` の 1 行目が結果 |
