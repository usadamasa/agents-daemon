---
name: triaging-issues
description: >-
  agents-daemon の issue と PR にラベル (種類、機能の area、優先度、epic) を付ける・付け直すとき、
  機能を足したり廃止したりして area のラベルを見直すときに使う。
  「issue を整理して」「ラベルを付けて」「triage して」「release note の分類がおかしい」で引く。
---

# issue と PR にラベルを付ける

agents-daemon の issue は 4 つの軸のラベルで分ける。ラベルの定義の実体は GitHub のラベル (`gh label list`) が持つ。

| 軸 | ラベル | 付け方 |
| ---- | ---- | ---- |
| 種類 | `bug` `enhancement` `documentation` `refactor` `investigation` | issue に 1 つ |
| 機能 | `area: <機能>` | 1 つ以上。廃止した機能の issue には付けない |
| 優先度 | `priority: high` `priority: medium` `priority: low` | 利用者が付ける |
| epic | `epic` | sub-issue を束ねる親 issue に付ける |

PR には種類と area が closing issue から自動で写り、release note の category になる (手順 6)。

## 手順

### 1. ラベルの足りない issue を出す

```sh
gh issue list --state all --limit 1000 --json number,state,title,labels --jq '.[] | select(([.labels[].name | select(test("^(bug|enhancement|documentation|refactor|investigation)$"))] | length) == 0 or ([.labels[].name | select(startswith("area: "))] | length) == 0) | "\(.number)\t\(.state)\t\([.labels[].name] | join(","))\t\(.title)"'
```

area が無い closed issue のうち、廃止した機能のもの (下の表の「廃止した機能」) はそのままでよい。
題で決めきれない issue は `gh issue view <番号>` で本文を読む。

### 2. 種類を決める

| ラベル | 当てる issue |
| ---- | ---- |
| `bug` | 今ある振る舞いが期待と違う。誤検知、取り違え、落ちる、競合 |
| `enhancement` | 新しい機能か、使い勝手と性能の改善 (結果は同じで、呼び出しや待ちを減らすものを含む) |
| `refactor` | 振る舞いを変えない整理。パッケージ構成、重複の除去、lint の違反、廃止した機能の撤去 |
| `documentation` | docs、skill、AGENTS.md だけを変える |
| `investigation` | 結論を issue のコメントに残して閉じ、実装は別の issue に切るもの。検討、triage、前提の裏取り |

### 3. area を決める

機能の区分は README の「役割」に合わせる。複数の機能に掛かる issue には当たる area をすべて付ける。

| area | 含むもの |
| ---- | ---- |
| `area: rate-limit` | 利用上限の検知と解除後の再開。`monitor`、`detect`、`ratelimit`、`simulate` |
| `area: statusline` | statusline の描画と state file への集約。`statusline`、`sessionstate`、`sessioncontext`、`ingest-statusline` |
| `area: dream` | 記憶階層の consolidation。`dream` のコマンドと skill、harness からの取り込み候補 |
| `area: deps-graph` | 依存図と agent-tasks アプリ (`gui/`)、タスクの記録 (`tasklog`、`ingest-task`)、辺 (`edge`)、`deps` |
| `area: parking` | パーキングロット。`parking`、`park` の tool とコマンド、ファイルのメモの通知 |
| `area: handoff` | peer セッションへの送達。`tell`、`spawn`、`park hand`、peer-sessions skill、handoff-notice |
| `area: rex` | rex の backend (`rexcli`、`multiplexer`) と、rex の前提への追従 |
| `area: housekeeping` | worktree の回収と保持期間での掃除。`worktree`、`daemon/housekeeping.go` |
| `area: lifecycle` | バイナリの用意と更新、daemon の起動と入れ替え、`doctor`、setup skill、CLI 共通のオプション |
| `area: mod` | Claude Code の mod (`hooks/*.ts`、`hooks/*.tsx`) の仕組みそのものと、本体の拡張点 (mod、MCP) を daemon に取り込む検討 |
| `area: coordination` | 互いを知らない独立したセッション同士の気づき合い、予約と排他、仕事の配分。送達の中の排他は `area: handoff` |
| `area: dev` | CI、lint、テスト、パッケージ構成、TLA+ のモデル、保守用の skill と docs |

見た目が変わっても機能が続いているなら同じ area に入れる。ボードと pane の issue は `area: deps-graph` に入る。
廃止した機能には area が無い。herdr、daemon の idle compact、ttl-guard、prompt cache の TTL の表示と ack、
PreModelSwitch、domain (私用と業務の境界) がこれにあたる。
廃止した機能の issue が open で残っていたら、area を付けずに close の候補として利用者に知らせる。
今ある機能の issue で当たる行が無いときは、近い area を付けて、area を足すかを利用者に確かめる。

### 4. ラベルを付ける

issue ごとではなく、ラベルごとに番号をまとめて付ける。

```sh
gh issue edit 392 393 395 --add-label "area: deps-graph"
```

### 5. epic を組む

親にする issue に `epic` を付け、子を sub-issue でつなぐ。

```sh
gh issue edit 88 --add-label epic
gh issue edit 88 --add-sub-issue 90,91
```

親の issue を新しく起票するのは、利用者が指示したときだけにする。close した epic に open の子が残っているときは、
付け替え先の候補を添えて利用者に知らせる。

### 6. PR のラベルを確かめる

PR を作るときの決めごとは AGENTS.md が持つ。PR が本文の `Closes #N` で issue を閉じるなら、pr-labels workflow が PR の open・edit・push のたびに
`scripts/sync-pr-labels.sh` を呼び、issue の種類と area を PR へ足す。epic と priority は写らない。
issue を閉じない PR には、種類と area を `gh pr edit <番号> --add-label ...` で直接付ける。

tagpr の Release PR の本文と GitHub Release の note は、`.github/release.yml` が種類のラベルで category に分ける。
Release PR の本文で「Other」に入った PR は種類が付いていないので、`gh pr edit` で足す。
Release PR の本文は次に main へ push されたときに作り直され、GitHub Release の note は tag を打つときに作られる。

### 7. 確かめる

手順 1 のコマンドを打ち直し、open の issue が 1 件も出ないことを見る。

## 機能を足す・廃止する

機能を足したら area のラベルを作り、手順 3 の表に行を足す。色は area で揃える。

```sh
gh label create "area: <機能>" --color 1d76db --description "<含むものを 1 句>"
```

機能を廃止したら手順 3 の表から行を外し、廃止した機能の列挙へ足す。ラベルを消すか残すかは利用者に確かめる
(消すと closed issue からも外れる)。

## APPENDIX: なぜこの形にするか

優先度はいつ着手するかの判断で、題と本文からは決まらないので利用者に残す。

release note の category は PR のラベルで決まり、issue のラベルは見ない。PR ごとに手で付けると漏れるので、
closing issue から写す。写すのは足すだけにし、PR に手で付けたラベルを消さない。
