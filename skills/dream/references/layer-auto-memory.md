# auto memory レイヤーの手順

対象は `~/.claude/projects/<project-canonical>/memory/` 配下の索引 (`MEMORY.md`) と各メモリ本体。

思想は SKILL.md の「統合の思想」にある。ここはその適用手順を持つ。

## 行き先を決める

全 memory に対して行き先を 1 つ決める。「そのまま残す」を選べるのは、下記「手順」の 5 の条件
(その知見が必要になる場面を skill の description に書けない) を満たすときだけ。

| memory の性質 | 行き先 |
|---|---|
| 一回限りのメモ・進行状態・ephemeral な記録 | 削除 (下記「削除する対象」) |
| domain overlap する既存 skill がある | その skill の body へ統合 |
| 反復する domain だが該当 skill が無い | skill を新規作成 (下記「スコープの選び方」) |
| 普遍的な作業規範で 1〜2 行に圧縮できる | グローバル CLAUDE.md の原本へ昇格 ([layer-claude-md](layer-claude-md.md)) |
| 特定リポジトリでしか成立しない | そのリポジトリの CLAUDE.md / AGENTS.md |
| 機械で拾える表記・手順の規範 | hook か prh ルール (`writing-feedback` skill が先例) |
| 設定値の意図 | 設定の原本のコメント |
| 上のどれでもない (手順 5 の条件を満たす) | memory に残し、本文へ失効条件を書く |

新規 skill を作ると判定したら、SKILL.md の作成そのものは `writing-skills` skill へ渡す。dream は
「skill 化すべき」と判定するところまでを担当する。

## スコープの選び方

| 条件 | 置き場 |
|---|---|
| 社内固有の情報 (社内サービス名・URL・社内プロセス) を含む | `caddi-skill-manager` の配置基準に従う (公開リポジトリには置かない) |
| 特定リポジトリの Taskfile やパスに依存する | そのリポジトリの `.claude/skills/` |
| 上記以外 (汎用的なツールの使い方・個人のワークフロー) | user scope のグローバル skill (`dotclaude/skills/`) |

## 手順

1. 各 memory の内容 domain を 1 文で要約する。
2. 利用可能な skill を走査し、**topic keyword** (例: sandbox, permission, shell, git) で domain overlap
   する skill をピックアップする。skill の一覧は `~/.claude/skills/*/SKILL.md` の description か `/skills`
   で得る。memory 名との substring 一致に頼らない。
   `agents-daemon dream mine` は名前の一致で association を絞るため取りこぼしがある。取りこぼしは
   `kind: unhomed` として stage レポートに 1 行出るので、この走査で行き先を埋める
   (判定基準は [skill-mining-heuristics](skill-mining-heuristics.md))。
3. 該当 skill があれば adversarial に問う: **「この skill が triggering された場面で、memory 内容は
   skill body に載っていた方が到達しやすいか?」**。「memory 独自の詳細だから残す」で判断停止しない。
4. YES なら skill へ統合する。memory 内容の universal 部分は skill body へ、project 固有部分は
   project CLAUDE.md へ振り分ける。統合後に memory を削除する。他の memory からの `[[memory-name]]`
   参照は skill 名参照へ置き換える。
5. 該当 skill が無いとき、または手順 3 の答えが NO のときは、行き先テーブルを上から順に当てる。
   最後の「memory に残す」を選べるのは、その知見が必要になる場面を skill の description に
   書けないときだけ。書けるなら skill にする。description がトリガー条件を持ち、必要なときだけ
   ロードされるので、memory より到達しやすい。
   残すと決めたら、本文へ失効条件 (この知見はいつ不要になるか) を 1 文書く。

## 索引と本文の保ち方

索引は常時ロードされるため lean に保つ。

- `MEMORY.md` は **1 ファイル 1 行フック** が原則。200 行を超えたら、複数行で書いている項目を
  子ファイルへ追い出す。
- 各メモリ本文の frontmatter (`name`, `description`, `metadata.type`) を最新化する。
  `originSessionId` と `node_type` は Claude Code 本体が保存時に自動付与する管理項目なので
  触らない ([auto-frontmatter](auto-frontmatter.md))。

## 削除する対象

行き先テーブルで他のレイヤーへ移したものに加えて、次は無条件で削除する。拾わない方針を取るだけでは、
過去の dream で拾ったものが残り続ける。

- 一回限りのデバッグメモ
- 特定タスクの進行状態
- ephemeral な作業ファイルの記録 (旧バージョンのハーネスや廃止されたユーティリティが書き出していた
  session 局所のスナップショットなど)
- 完了済みで参照価値がないもの (「PR #X で対応済み」のような時限性のものは特に)

## 削除の方法

memory ディレクトリは Claude Code 組み込みの sandbox 保護で **bash の `rm` が
`Operation not permitted` になる**ため、`agents-daemon dream forget <name>...` を使う。MEMORY.md の索引行も
同時に除去される。消す前に `--dry-run` で対象を確認する。

`rm` そのものを `sandbox.excludedCommands` に足さない。`rm` を sandbox の外に出すと OS の保護を捨てる範囲が
広すぎる。`dream forget` は削除できる範囲を memory ディレクトリ直下の `.md` に閉じているので、こちらだけを
除外する (登録する行は `agents-daemon:setup` skill の「dream forget の sandbox 除外」)。
