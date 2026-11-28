---
name: catching-up-with-rex-changelog
description: >-
  端末多重化ツール rex が更新されたあと、agents-daemon が rex に置いている前提 (`rex-premise` marker) を
  changelog と実機の応答と突き合わせて追従するときに使う。rex を上げた、rex の changelog を読みたい、
  rex を上げてから daemon の挙動が変わった、rex の新機能を daemon に取り込めないか見たい、といった場面。
  「rex の changelog を追って」「rex を上げたので点検して」「rex の前提が古くなっていないか」で引く。
---

# rex の changelog に追従する

agents-daemon の rex backend (`rex`) は rex の control API の応答と event の形の上に建っている。record の `owner.pid`
は OSC 7501 の仕様に無い rex 独自の欄で、対象が無いときの error の code や、新しい session の最初の block に
`block_created` が来ないことも実測。spawn の包みだけは pane の中から rex の CLI を叩く。rex は数日おきに
preview が出るので、上げたら changelog と実機の応答をこの repo の前提と突き合わせる。

| 資料 | 何が分かるか | 読み方 |
| ---- | ---- | ---- |
| changelog (superlogical MCP) | 版ごとの変更。投稿は platform ごと (Mac、Server & CLI、iOS) に分かれる | `list_changelog` の一覧から範囲の投稿を `read_changelog` に ID で渡して 1 投稿ずつ本文を読む。`search_changelog` は検索語の裏取りに使う |
| 実機の control API (自分の block) | 応答の JSON の欄、error の code、API revision | `doctor` の「rex の前提」の節が固定の呼び出しを打って欄を名指しで出す。event と一覧の増減だけ人が見る ([references/api-checks.md](references/api-checks.md)) |
| server の docs (`/llms.txt`、`/api/help`) | method と event の schema、API revision ごとの変更、event と応答を組み合わせる決めごと | 同じ socket から `curl --unix-socket` で取る ([references/api-checks.md](references/api-checks.md) の「人が見る行」) |
| rex docs | OSC 7501 の仕様 (何が仕様で何が rex 独自か) | [program-status](https://www.superlogical.com/rex/docs/build/program-status) |

前提: superlogical MCP (`list_changelog` / `read_changelog`) が繋がっていること、rex の block の中で動いていること
(確かめ方は `agents-daemon:setup` skill の 2 段目)。rex の場所の環境変数の前提は `rexplace` パッケージの marker が持つ。

着地は「ずれを issue にし、確かめ直した marker の `<version>` を上げる」。挙動が変わる対処をその場で実装しない
(既存の挙動を変える変更は利用者の判断)。

## 手順

### 1. 範囲を決める

```sh
go run ./cmd/agents-daemon doctor
```

「rex の前提」の節の `rex_version`、`built_at`、`api_revision` を控える (手順 4 の実機の点検もこの節が兼ねる)。
機械で読むなら `--json doctor | jq '.premises.findings'` で同じ節だけを取り出せる。
`rex_version` と `api_revision` は server (`server.version`) の値。同じ節が、server の対応する revision の幅に
daemon の revision が入るかを見て、外れれば `⚠` になる (幅を外れると daemon は rex に接続できない)。
`api_revision` が前回 (`last-checked.md`) から上がっていたら、手順 4 で `/llms.txt` の「API Revisions」を読む。

changelog の投稿は platform ごとに分かれ、`preview.N` の数列も platform ごとに別。手元の build に当たる投稿を
2 つ決める。

- **Server & CLI** (`Rex v0.1.0-preview.N (Server & CLI)`): server と CLI の版。doctor の `rex_version`
  (`0.1.0-preview.N`) と同じ `preview.N` の投稿を取る。spawn の包みが叩く CLI が server と同じ build かは
  `rex version --json` の `rex_version` が doctor の `rex_version` と同じことで確かめる。
  食い違えば server が旧 build のまま動いている (app の更新後に restart していない) ので、server を再起動して
  手順 1 をやり直す
- **Mac** (`Rex v0.1.0-preview.N-<build>-<hash> (Mac)`): app の版。本文の末尾の「Includes embedded server
  v0.1.0-preview.N」が Server & CLI の版と同じ投稿を取る。この Mac の `preview.N` が marker と testdata の
  `<version>` になる

marker を Mac の版のまま書くのは、2026-10 までの marker と last-checked.md が Mac の数列で並んでいて、
Server & CLI の `preview.N` と同じ数字が別の時期を指すため。iOS の投稿は読まない。

前回見た版は [references/last-checked.md](references/last-checked.md) の最後の行。範囲は Mac の列の次の版から
手元の Mac の版まで。同じ期間の Server & CLI の投稿も範囲に入る (前回の Server & CLI の列の次の版から)。
`rex_version` が前回と同じなら changelog は読まず、手順 4 の実機の点検だけ行う。

### 2. changelog を読む

範囲の投稿を `read_changelog` で 1 投稿ずつ読む。`release` には `list_changelog` の一覧の ID を渡す。版の文字列で
引くと Server & CLI と Mac の `preview.N` が衝突して別の投稿が返る。要約で済ませない (1 行の変更が前提を壊す)。
投稿ごとに読む範囲が違う。

- **Server & CLI**: 全文。`api`、`cli`、`program_status` の前提はほぼここに載る
- **Mac**: server の項目 (OSC 7501、program status、Kitty、「server restart」が要る項目)。app の UI の項目は飛ばす

読みながら 2 つの観点で拾う。

- **前提に当たるか**: 「前提の洗い出し」の topic の検索語に当たる項目
- **アイデアになるか**: daemon が polling や画面の読み取りでやっていることを rex が直接出せるようになった項目
  (block の新しい method や event、Lua の常駐の手段)

### 3. marker の一覧を出す

```sh
rg -n -o --hidden 'rex-premise: preview\.[0-9]+ (program_status|cli|api).*' --glob '!tmp/**' --glob '!.git/**' . | sort -t' ' -k3,3 -k2,2V
```

AGENTS.md の説明文の行は `rex-premise:` の後に版が続かないので、この pattern には当たらない。
`--hidden` は `.claude/skills/` の references にある marker を拾うため。
版を上げる置き換えは topic の語まで含めて打つ (`preview.22 cli` → `preview.25 cli`、`program_status` も同じ)。
`preview.22 ` のように空白で終わる文字列で置き換えると、Edit の new_string の末尾の空白が落ちて `preview.25cli` になる。
版を上げた後は、空白の欠けた marker (`preview.21cli`) が無いことを `rg -n --hidden 'rex-premise: preview\.[0-9]+[^ 0-9]' --glob '!tmp/**' .`
で確かめる。この pattern に当たる marker は上の一覧から漏れる。
`<version>` は Mac の版なので、範囲の始まりの Mac の版より古い marker が点検の対象。そういう marker が無ければ
changelog 側の点検は無しで、手順 4 へ進む (実機の点検は版に関係なく行う)。

### 4. 実機で応答の形を突き合わせる

changelog に該当が無くても行う。欄の追加や名前の変更は changelog に載らないことがある。

自分の block の ID は `rex --autostart=false whoami --json | jq -r .block_id` で先に取り、下のコマンドの `<block>` を
その値に置き換えて打つ (変数の展開を含むコマンドは、worktree 隔離セッションで guard が拒否する)。
Mac の版は app の `Info.plist` の `RexVersion` (`plutil -p /Applications/Rex.app/Contents/Info.plist`) で読む。

手順 1 で回した `doctor` の出力を 2 つの節で読む。

- 「pane 一覧」: 自分の block (whoami の `block_id`) の `pane=` 行を見る。`agent_status=working` で `unknown の理由` の行が
  付いていなければ、record の `owner.pid` と foreground の process の pid の突き合わせは通っている。
  Bash ツールの実行中は working になる。理由の行が出たら、その文面が欄の有無を言っている (`record に owner.pid が無い`)。
- 「rex の前提」: コードが読む欄と、対象が無いときの error の code を自分の block に打って名指しで出す。
  `⚠` の行がずれ。全部 `OK:` なら判断の要らない点検は済んでいる。

残りは人が見る。購読の event と、method / event の一覧と schema の増減、revision の変更は判断が要るので doctor に
入れていない。打つものと見る欄は [references/api-checks.md](references/api-checks.md) の「人が見る行」を Read で読む。
実機の JSON には fixture に無い欄がある (`list_blocks` の `owner` など)。それはずれではない。
ずれとして扱うのは、コードが読む欄の欠落・名前の違い・型の違い・error の code の違い。
spawn の包みが叩く CLI (`wrap` の `sessionScript` の `block ls` と `session kill`) は `cli` の marker が指す。
包みのテストの fake の rex (`wrap_fake_test.go`) が実機と同じ引数と exit code かを、marker の版が古いときに確かめる。

### 5. レポートを書いて issue にする

`./tmp/rex-drift/<YYYY-MM-DD>.md`。PR 用の worktree に入る前に、main の checkout の `./tmp` へ書く。
worktree の `./tmp` は `ExitWorktree` の remove で一緒に消え、worktree の中からは main の checkout へ書けない。
節は次の順。空の節も見出しだけ残す (「確認したが無かった」が伝わる)。

1. 前提が崩れた: どの marker (ファイル:行) が、changelog のどの項目か実機のどの応答で崩れたか。daemon への影響
   (全 pane が unknown、PaneList が落ちる、など)
2. 取り込める新機能 (アイデア): 何を置き換えるか、何が簡単になるかを 1〜2 行
3. 実機の点検: 表で「コマンド / 結果 / 判定」。変更不要も明示する
4. 確認して変更不要と判断したもの: 1 行ずつ根拠つき
5. 手順上の落とし穴: 次回の自分へ

1 と 2 は項目ごとに issue を立てる。利用者に起票するか聞かない (この skill を呼んだことが起票の指示で、レポートは
`./tmp` に残るだけなので issue にしないと消える)。issue の本文は `./tmp` に Write し `gh issue create --body-file`。
背景 (changelog の引用、実機の応答)、marker のある行、検証項目、レポートのパスを入れる。既存の issue と重ならないか
`gh issue list` で見てから立て、重なるものは新しい issue にせずその issue へ `gh issue comment --body-file` で足す。
レポートの要点と issue の番号を利用者へ伝える。

最後に 2 つを同じ PR に入れる。

- 確かめ直して変更不要だった marker の `<version>` を手元の Mac の版へ上げる。この差分が点検の証跡。ずれた marker は
  上げずに残す (issue の番号は last-checked.md の結果の列に書く)
- [references/last-checked.md](references/last-checked.md) に 1 行足す (日付、`rex_version`、Mac の版、
  Server & CLI の版、結果と issue の番号)

## 前提の洗い出し

前提の一覧を手で持たない。rex の版で変わりうる挙動を述べた箇所には marker が 1 行ある
(AGENTS.md「Claude Code の挙動を書くときの決めごと」)。

```text
rex-premise: <version> <topic> [メモ]
```

- `<version>`: その前提を最後に確かめたときの Mac の changelog の版 (`preview.N` の形)。Server & CLI の版は書かない
- `<topic>`: 下の表の 1 語
- Go と sh はコメント行、md は `<!-- rex-premise: … -->` の 1 行

| topic | 前提が支える機能 | changelog の検索語 |
| ---- | ---- | ---- |
| `program_status` | record から Agent と `agent_status` を決める | OSC7501, OSC 7501, program status, activity, owner, record, status report |
| `api` | daemon が control API で打つ method の引数と応答、error の code、event の payload、API revision | API revision, control, method, event, session.create, attach, format, target.resolve, not_found |
| `cli` | spawn の包みと peer-sessions skill (読む・送る・畳む) が叩く rex の CLI の引数と応答、exit code、`--autostart`、rex の置き場 | rex CLI, block ls, session kill, window close, capture, autostart, exit code, `--json` |

表に無い topic が出てきたら、この表と AGENTS.md の topic の列挙に足す。

## やらないこと

- 見つけたずれをその場で直す。挙動が変わるので利用者の判断。docs の食い違いも同じ PR に混ぜない。
- 範囲外の版や、Mac の投稿の UI の項目と iOS の投稿の整理。レポートが長くなるだけで判断材料は増えない。
- 他人の block にコマンドを打つ。点検は自分の block と、存在しない block ID だけで足りる。
- Discord の URL を組み立てる。MCP の結果に出た link だけを使う。
