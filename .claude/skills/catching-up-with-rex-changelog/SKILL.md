---
name: catching-up-with-rex-changelog
description: >-
  端末多重化ツール rex が更新されたあと、agents-daemon が rex に置いている前提 (`rex-premise` marker) を
  changelog と実機の応答と突き合わせて追従するときに使う。rex を上げた、rex の changelog を読みたい、
  rex を上げてから daemon の挙動が変わった、rex の新機能を daemon に取り込めないか見たい、といった場面。
  「rex の changelog を追って」「rex を上げたので点検して」「rex の前提が古くなっていないか」で引く。
---

# rex の changelog に追従する

agents-daemon の rex backend (`internal/rexcli`) は rex の CLI の応答の形の上に建っている。record の `owner.pid`
は OSC 7501 の仕様に無い rex 独自の欄で、`block call` の exit code や `block ls` の `-s` 必須も実測。rex は数日おきに
preview が出るので、上げたら changelog と実機の応答をこの repo の前提と突き合わせる。

| 資料 | 何が分かるか | 読み方 |
| ---- | ---- | ---- |
| changelog (superlogical MCP) | 版ごとの変更。「rex CLI」「Rex Server」の節に CLI と program_status の変更が載る | `read_changelog` で範囲の版を 1 版ずつ全文読む。`search_changelog` は検索語の裏取りに使う |
| 実機の CLI (自分の block) | 応答の JSON の欄、exit code、flag の有無 | [references/cli-checks.md](references/cli-checks.md) の固定コマンドを打ち、コードが読む欄と突き合わせる |
| rex docs | OSC 7501 の仕様 (何が仕様で何が rex 独自か) | [program-status](https://www.superlogical.com/rex/docs/build/program-status) |

前提: superlogical MCP (`list_changelog` / `read_changelog`) が繋がっていること、rex の block の中で動いていること
(`REX_SESSION` と `REX_BLOCK` がある)。

着地は「ずれを issue にし、確かめ直した marker の `<version>` を上げる」。挙動が変わる対処をその場で実装しない
(既存の挙動を変える変更は利用者の判断)。

## 手順

### 1. 範囲を決める

```sh
rex version --json
```

`rex_version` と `built_at` を控える。rex の CLI は changelog の版 (`preview.N` の形) を出さない
(`api call server.version` も同じ)。changelog の版は `list_changelog` の一覧から、`built_at` の日付と同じ日かその直後の
post を取る。post は build より後に出る。

前回見た版は [references/last-checked.md](references/last-checked.md) の最後の行。範囲はその次の版から今の版まで。
`rex_version` が前回と同じなら changelog は読まず、手順 4 の実機の点検だけ行う。

### 2. changelog を読む

範囲の版を `read_changelog` で 1 版ずつ読む。要約で済ませない (1 行の変更が前提を壊す)。読みながら 2 つの目で拾う。

- **前提に当たるか**: 「前提の洗い出し」の topic の検索語に当たる項目。「rex CLI」「Rex Server」の節が本命で、
  macOS App の節は飛ばしてよい
- **アイデアになるか**: daemon が polling や画面の読み取りでやっていることを rex が直接出せるようになった項目
  (block の新しい method や event、Lua の常駐の手段)

### 3. marker の一覧を出す

```sh
rg -n -o 'rex-premise: preview\.[0-9]+ (program_status|cli).*' --glob '!tmp/**' . | sort -t' ' -k3,3 -k2,2V
```

AGENTS.md の説明文の行は `rex-premise:` の後に版が続かないので、この pattern には当たらない。
`<version>` が範囲の始まりより古い marker が点検の対象。全部が今の版なら changelog 側の点検は無しで、手順 4 へ進む
(実機の点検は版に関係なく行う)。

### 4. 実機で応答の形を突き合わせる

changelog に該当が無くても行う。欄の追加や名前の変更は changelog に載らないことがある。

worktree 隔離セッション (EnterWorktree の後) では、`$REX_BLOCK` と `$REX_SESSION` の展開を含む Bash コマンドを
guard が拒否する。先に `printenv REX_BLOCK REX_SESSION` で値を取り、下のコマンドの変数をその値に置き換えて打つ。

まず今のコードの `doctor` で自分の pane を見る。

```sh
go run ./cmd/agents-daemon doctor
```

自分の block (`REX_BLOCK` の値) の `pane=` 行が `agent_status=working` で、`unknown の理由` の行が
付いていなければ、record の `owner.pid` と前景の pid の突き合わせは通っている。Bash ツールの実行中は working になる。
理由の行が出たら、その文面が欄の有無を言っている (`record に owner.pid が無い`)。

次に固定のコマンドを打ち、コードが読む欄だけを突き合わせる。コマンドの一覧と、読む欄・突き合わせる先・コマンドごとの
注意は [references/cli-checks.md](references/cli-checks.md) を Read で読む。実機の JSON には fixture に無い欄がある
(`block ls` の `owner` など)。それはずれではない。
ずれとして扱うのは、コードが読む欄の欠落・名前の違い・型の違い・exit code の違い。

### 5. レポートを書いて issue にする

`./tmp/rex-drift/<YYYY-MM-DD>.md`。節は次の順。空の節も見出しだけ残す (「確認したが無かった」が伝わる)。

1. 前提が崩れた: どの marker (ファイル:行) が、changelog のどの項目か実機のどの応答で崩れたか。daemon への影響
   (全 pane が unknown、PaneList が落ちる、など)
2. 取り込める新機能 (アイデア): 何を置き換えるか、何が簡単になるかを 1〜2 行
3. 実機の点検: 表で「コマンド / 結果 / 判定」。変更不要も明示する
4. 確認して変更不要と判断したもの: 1 行ずつ根拠つき
5. 手順上の落とし穴: 次回の自分へ

レポートの要点を利用者へ伝え、1 と 2 を issue にするか決めてもらう。issue の本文は `./tmp` に Write し
`gh issue create --body-file`。背景 (changelog の引用、実機の応答)、marker のある行、検証項目、レポートのパスを入れる。
既存の issue と重ならないか `gh issue list` で見てから立てる。

最後に 2 つを同じ PR に入れる。

- 確かめ直して変更不要だった marker の `<version>` を今の版へ上げる。この差分が点検の証跡
- [references/last-checked.md](references/last-checked.md) に 1 行足す (日付、`rex_version`、changelog の版、結果)

## 前提の洗い出し

前提の一覧を手で持たない。rex の版で変わりうる挙動を述べた箇所には marker が 1 行ある
(AGENTS.md「Claude Code の挙動を書くときの決めごと」)。

```text
rex-premise: <version> <topic> [メモ]
```

- `<version>`: その前提を最後に確かめた changelog の版 (`preview.N` の形)
- `<topic>`: 下の表の 1 語
- Go と sh はコメント行、md は `<!-- rex-premise: … -->` の 1 行

| topic | daemon の役目 | changelog の検索語 |
| ---- | ---- | ---- |
| `program_status` | record から Agent と `agent_status` を決める | OSC7501, OSC 7501, program status, activity, owner, record, status report |
| `cli` | `block call` / `block ls` / `capture` / `send` の引数と応答、exit code、`--autostart` | rex CLI, block call, block ls, capture, send-key, autostart, exit code, `--json`, events |

表に無い topic が出てきたら、この表と AGENTS.md の topic の列挙に足す。

## やらないこと

- 見つけたずれをその場で直す。挙動が変わるので利用者の判断。docs の食い違いも同じ PR に混ぜない。
- 範囲外の版や、macOS App の UI の項目の整理。レポートが長くなるだけで判断材料は増えない。
- 他人の block にコマンドを打つ。点検は自分の block (`$REX_BLOCK`) と、存在しない block ID だけで足りる。
- Discord の URL を組み立てる。MCP の結果に出た link だけを使う。
