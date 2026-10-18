---
name: dream
description: >-
  Claude Code の記憶階層 (CLAUDE.md、auto memory、skill) を横断して重複・矛盾・陳腐化を除く consolidation。
  記憶ファイルが肥大化した、同じことが複数箇所にある、参照先が古い、skill が triggering しないと感じたときに使う。
---

<!-- writing-gate-disable pink-elephant -->

<!-- 除去対象のパターンを例示として本文に持つため、該当ルールだけ無効化する｡ -->

# dream: Claude Code 記憶階層と skill の consolidation

記憶階層 (CLAUDE.md / auto memory / skill) を横断して、知見の置き場所を決め直すスキル。
Anthropic Managed Agents の Dreams (auto-dream) を git ベース・手動運用で再現する。

本体は統合の思想と進め方を持つ。レイヤーごとの手順と実行系は references に分けてある。

原典: [@conao_3 / Fable リリース時の memory-dream playbook](https://x.com/conao_3/status/2065815666310238330)。
Claude Code 環境 (CLAUDE.md / auto memory / skills) に翻訳し、2026-06-20 時点の Dreams API 仕様で補強。

## 統合の思想

**定義箇所はひとつ**。同じ知見が 2 か所にあるなら下位側を削る。上位レイヤが定めるルールを下位で
再掲しない。必要なら、下位から「所在だけを 1 句で指す」のは許容する。

**ロードされる保証のないものを参照しない**。CLAUDE.md が auto memory のメモリ名を直接指す形も、
グローバル CLAUDE.md が特定リポジトリのファイルを名指しする形も作らない。どちらも、参照した側が
そのセッションで完結しなくなる。参照したくなったのは、その記述の置き場所を間違えたということ。
skill 名での参照は別扱いで、description でトリガーされて挙動が予測できるため健全。

**memory は恒久の置き場ではない**。auto memory は次の consolidation までの一時置き場として扱う。
dream を回すたびに全 memory へ行き先を 1 つ決め、残すと決めたものだけを残す。

**昇格と剪定は対にする**。下位から上位へ動かした分だけ上位が太る。昇格させた dream の中で上位側の
剪定まで行い、知見が上へ流れるだけの状態にしない。

**入力非破壊・採用前レビュー**。dream の出力には hallucination が混じりうる。削除も追記も論理単位
ごとの commit に分け、ユーザーが diff を見て判断できる状態で止める。push は明示指示まで保留する。

consolidation は fine-tune ではなく記憶の再編。モデルや skill の挙動そのものは変えない。

## レイヤーごとの手順

| レイヤー | 決めること | reference |
|---|---|---|
| グローバル / プロジェクト CLAUDE.md | 重複の削り方、昇格の条件、剪定 | [layer-claude-md](references/layer-claude-md.md) |
| auto memory (索引と本体) | 全 memory の行き先、削除 | [layer-auto-memory](references/layer-auto-memory.md) |
| skill (SKILL.md と description) | body への受け入れ、description の増減、変更の検証 | [layer-skills](references/layer-skills.md) |

階層そのものの地図 (パスとロード順、dream の対象範囲) は
[memory-layers](references/memory-layers.md)。実行系 (7-phase pipeline、`agents-daemon dream` の
呼び方、実体パスの解決) は [pipeline](references/pipeline.md)。背景は
[why-consolidate](references/why-consolidate.md)。

設定 (`settings.json`)・hook・エージェントは dream の対象外。`claude-config-management` と
`permission-optimizer` が担う。

## 進め方

### Step 1: Mine (採掘)

直近セッションの transcript・git log・auto memory の更新時刻から、次のものを抽出する。

- 繰り返し受けた指摘 (auto memory の feedback として残すべきもの)
- 確定した運用ルール・方針転換
- 新しく見つかった事実 (ツールの挙動・制約・落とし穴)
- ハーネスのネイティブ機能と被って役目を終えた自作ユーティリティ (`/release-notes` や CHANGELOG で確認)

**拾わず、既に memory にあれば削除するもの**: 一回限りのデバッグメモ、特定タスクの進行状態、
ephemeral な作業ファイル。作業ファイルの例は、旧バージョンのハーネスや廃止されたユーティリティが
書き出していた session 局所のスナップショット。拾わないだけでは、過去の dream で拾ったものが残り続ける。
削除は Step 3 で行う ([layer-auto-memory](references/layer-auto-memory.md))。

### Step 2: Consolidate (統合)

抽出物を既存記憶へマージしつつ、以下を強制する。

- **日付への依存を落とす**: 「昨日」「先週」「今月」は未来のセッションで解決できない。まず日付ごと
  削れないかを考える。知見の本体が「いつ」に依らないなら日付は要らない。日付そのものが情報のとき
  (プロジェクトの期日、仕様が切り替わった時点) だけ `2026-06-20` の形へ直す。
- **矛盾の最新値解決**: 同じトピックで古い記述と新しい記述が両立しているなら、新しい値で置き換える。
  古い記述は削除する (経緯は commit message へ書けばよく、本文に残さない)。
- **存在しない参照の除去**: 言及されているファイル・関数・フラグ・PR が現存するか `grep` /
  `gh pr view` で確認する。無ければ削除、改名されていれば更新。

### Step 3: Dispose (行き先の決定)

全 memory に対して行き先を 1 つ決める。判定は
[layer-auto-memory](references/layer-auto-memory.md) の行き先テーブルに従う。
`agents-daemon dream stage` が出す行き先表の空欄を、この Step で埋める。

昇格先が CLAUDE.md なら、同じ dream の中で
[layer-claude-md](references/layer-claude-md.md) の剪定まで行う。統合先が skill 本体なら、
[layer-skills](references/layer-skills.md) の検証を通す。

どちらが正しいか判別できない矛盾があるときだけ、ユーザーに確認する。

### Step 4: Prune & Index (剪定・索引化)

常時ロードされるものを lean に保つ。各レイヤーの具体は reference が持つ。

- memory 索引と本文の frontmatter: [layer-auto-memory](references/layer-auto-memory.md)
- CLAUDE.md の剪定: [layer-claude-md](references/layer-claude-md.md)
- skill の description: [layer-skills](references/layer-skills.md)

## 成果ファイルの書き方 (重要)

判断のメタと経緯は **この skill / commit message / PR 本文** に置き、**成果ファイル
(CLAUDE.md / auto memory / SKILL.md) には書かない**。成果ファイルから除くものは次のとおり。

- **重複回避の注記**: 「これは X が定める、ここでは重複させない／再掲しない」等のメタ説明。
  重複は黙って消すだけでよい。
- **経緯・履歴**: 失敗談、「ユーザーに指摘された」「繰り返し外している」「同じ指摘を N 回受けた」、
  学習日 (`[2026-01-15]` 等のマーカー)、「PR #N で revert 済み」のような時限注記。frontmatter の
  `originSessionId` は Claude Code 本体が管理する自動項目なので、dream の手動削除対象には含めない。

  auto memory の `**Why:**` / `**How to apply:**` 句そのものは Claude Code の memory 仕様が要求する
  構造なので**消さない**。消すのは Why の中身に混ざった incident narrative の方で、行動を変える
  技術的因果だけを残して書き直す。
- **過去判断の上書き注記**: 「以前は X だったが今は不要」のような経緯注記。

結果として残すのは、**現行で正しい知見・ルール・再現手順** だけ。

**例外として残してよいもの**: 行動を変える技術的因果 (「A だと B が壊れるので C する」)。これは
知見の本体。ただし「誰がいつ何を指摘したか」までは残さない。

## チェックリスト

dream を実施したセッションで以下を確認する。終わったらユーザーにチェック結果を提示する。

- [ ] 日付への依存を落とした (削れない日付だけ絶対日付へ直した)
- [ ] 上位が定めるルールを下位から削った (重複回避の注記自体も残していない)
- [ ] CLAUDE.md から auto memory を名指しする記述がない (あれば直書きまたは skill 化に変換した)
- [ ] グローバル CLAUDE.md が特定リポジトリのファイルを名指ししていない
- [ ] 成果ファイルから経緯・履歴 (失敗談 / 指摘された旨 / 再発回数 / 学習日) を除いた (auto memory の `**Why:**` 句の構造と、Claude Code 本体管理の `originSessionId` / `node_type` は対象外)
- [ ] 矛盾を最新値で解決した (曖昧ならユーザー確認した)
- [ ] 存在しないファイル / シンボル / PR への参照を除去 or 現存確認した
- [ ] ネイティブ機能と被って役目を終えた自作ユーティリティへの言及を整理した
- [ ] `MEMORY.md` が lean (200 行未満・1 ファイル 1 行フック)
- [ ] グローバル/プロジェクト CLAUDE.md が「世界のルール」と「プロジェクト固有」で適切に切り分けされている
- [ ] CLAUDE.md へ昇格させた分の剪定を同じ dream の中で行った (追記だけで終わっていない)
- [ ] スキルと CLAUDE.md の重複が解消されている (CLAUDE.md が「スキルに従う」と参照する形)
- [ ] stage レポートの全 memory 行に行き先が入っている (`unhomed` を空欄のまま残していない)
- [ ] memory に残したものは、その知見が必要になる場面を skill の description に書けないことを確認し、本文へ失効条件を書いた
- [ ] 変更を論理単位ごとに commit し、ユーザーが diff レビュー可能 (push は保留)
- [ ] 採用前に diff レビューを依頼した (dream 出力は hallucination 混入の懸念があるため鵜呑み禁止)

## トリガー条件 (いつ実施するか)

- **大規模リファクタ直後** (リネーム多数・ツール移行・ディレクトリ再編・ハーネスバージョンアップ)。
  古いエントリが混乱を増やすため最優先。
- **セッション数の蓄積時** (Anthropic Dreams のデフォルト目安は 24h + 5 セッション、実務的には
  20〜30 セッションでノイズ化する)。
- **記憶ファイルが特定の閾値を超えたとき**:
  - `MEMORY.md` が 200 行超
  - 1 つの auto memory ファイルが 100 行超 (細分化するか剪定するか判断)
  - CLAUDE.md がスクロールしないと全体を見渡せない
- **ユーザーが明示指示** (「記憶を整理して」「dream」「memory consolidation」「auto memory 棚卸し」)。

## 参考

- [Dreams — Claude API Docs](https://platform.claude.com/docs/en/managed-agents/dreams): 公式。`managed-agents-2026-04-01` + `dreaming-2026-04-21` beta header、対応モデル Opus 4.7 / Sonnet 4.6 / Fable 5、入力最大 100 セッション、`instructions` 4096 文字
- [Auto-dream mechanics (claudefa.st)](https://claudefa.st/blog/guide/mechanics/auto-dream)
- [grandamenium/dream-skill](https://github.com/grandamenium/dream-skill): 4 フェーズ consolidation の OSS 再現
