# agents-daemon dream (CLI の実装リファレンス)

`dream` skill が使う Go の実行系。agents-daemon のバイナリの `dream` サブコマンドとして、
`harvest` / `mine` / `stage` / `adopt` / `forget` の 5 つを提供する。

本ファイルはどう動くかの実装リファレンス。いつ・何のために実行するかは skill 本体が持ち、
呼び方 (絶対パス、sandbox の除外) は [pipeline.md](pipeline.md) にある。

## Overview

| Subcommand | Phase | 役割 |
| --- | --- | --- |
| `dream harvest` | 1 Harvest | session JSONL 走査 + PII 除去 → harvest jsonl |
| `dream mine` | 2 Mine | harvest + memory + skill → candidates.jsonl |
| `dream stage` | 6 Stage | candidates → Markdown 承認レポート |
| `dream adopt` | 7 Adopt | candidate → git branch 作成 + LLM 向け手順書 |
| `dream forget` | 4 Consolidate | auto memory 削除 + MEMORY.md 索引の同期 |

## state の置き場

`${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/` (以下 `$DREAM`)。agents-daemon の他の state と同じ規則で
XDG の state に置き、`~/.claude/` には書かない。

| パス | 書く主体 | 中身 |
| --- | --- | --- |
| `$DREAM/harvest/YYYY-MM-DD.jsonl` | `dream harvest` | 1 セッション 1 行の集約 (session_id で dedup、last-wins) |
| `$DREAM/harvest-state.json` | `dream harvest` | `--incremental` の前回実行時刻 |
| `$DREAM/candidates/<project-slug>-YYYY-MM-DD.jsonl` | `dream mine` | 候補 |
| `$DREAM/replays/<candidate-id>.json` | skill の Replay phase (LLM) | 旧新の挙動予測 |
| `$DREAM/verdicts/<candidate-id>.json` | skill の Gate phase (LLM) | 3 judge の最終 verdict |
| `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/logs/dream-harvest.log` | Stop hook が起こした `dream harvest` | 失敗の stderr (成功時は無音) |

`harvest/` `candidates/` `replays/` `verdicts/` の 90 日より古いファイルは `dream harvest` が書き込みの前に
mtime で消す (`internal/dream` の `Prune`)。daemon の housekeeping は rex の外では走らないので、
Stop のたびに起きる harvest が書き手として掃除する。

## `dream harvest`

Claude Code の session JSONL を走査し、PII を除去して `$DREAM/harvest/` に集約する。cwd 非依存
(Stop hook から任意 project 下で呼ばれる)。

### Flags

| Flag | Default | 説明 |
| --- | --- | --- |
| `--projects-dir` | `~/.claude/projects` | Claude Code プロジェクトディレクトリ |
| `--days` | `14` | 走査する直近日数 (mtime ベース) |
| `--project-glob` | `""` | 特定プロジェクトに絞る (canonical name substring 一致) |
| `--incremental` | `false` | 前回 harvest 以降に更新されたセッションのみ処理 |
| `--detach` | `false` | 同じ引数で自分を detach 起動し、待たずに戻る (Stop hook 用) |
| `-v`, `--verbose` | `false` | 処理サマリを stderr に出力 |

### Schema (各 harvest レコード)

- `session_id`, `ts_start`, `ts_end`, `cwd`, `cwd_project`, `n_messages`
- `user_prompts`: 最大 10 件、200 rune で truncate、PII 伏せ字化済み
- `skills_invoked`: 呼ばれた skill 名 (重複除去、順序保存)
- `feedback_signals`: 「そうじゃなくて」「stop doing X」等の user correction (`{quote, kind}`)
- `failed_invocations`: skill invoked 直後に user が訂正したケース (`{invoked_skill, user_reaction}`)

### PII 伏せ字化ルール

次のパターンを機械検出して `[REDACTED]` に置換する。

- `[A-Z]{2}-[A-Z]{2,6}-\d{2,6}`: 候補者 ID
- email アドレス
- 日本の携帯電話番号

氏名は regex で捕捉できないため上位運用と組み合わせる。

### Stop hook 連携

plugin の `hooks/dream-harvest.sh` (Stop) が `dream harvest --incremental --detach` を exec する。hook が担うのは
バイナリの場所の解決だけで、バイナリが無ければ何もしない。detach・ログ・失敗の行の時刻付けはバイナリ側。

hook は transcript を読まない。読むのは起こしたバイナリで、次の前提で設計している。

- 走査は `~/.claude/projects` 配下の JSONL を mtime で絞り、session_id ごとに last-wins で書き直す
- Stop の時点でまだ書き切れていないセッションは、次の harvest (どのセッションの Stop でもよい) で読み直される
- harvest が拾うのは user prompt と Skill の tool_use で、ターンの最後の assistant 応答は使わない

## `dream mine`

対象 project の feedback memory と skill 本体を突き合わせて、`memory → skill body への migration 候補` と
`重複 memory の除去候補` を抽出する。

### 入力

- harvest jsonl (対象 project セッション)
- `~/.claude/projects/<canonical>/memory/*.md` (feedback memory)
- `~/.claude/skills/<name>/SKILL.md` (global skill 本体)
- `<project_root>/.claude/skills/<name>/SKILL.md` (fallback、project-local skill)

### Coverage 判定 (3 段階)

1. 完全 substring match
2. token match 60%+
3. 4-rune shingle match 40%+

### 分類ルール

association 先 skill が見つからない memory は `unhomed` (`confidence: unknown`) として出す。

association 先がある memory は coverage ratio で `skill_body` / `memory_dedup` ×
`high` / `medium` / `low` に分ける。閾値は次のとおり。

- `≥95%` → memory_dedup high
- `≥80%` → memory_dedup medium
- `≤15%` → skill_body high
- `≤40%` → skill_body medium
- それ以外 → skill_body low

閾値の spec は [skill-mining-heuristics.md](skill-mining-heuristics.md) を single source of truth とする。
閾値を変えるときは、この spec と `internal/dream/mine/coverage.go` の定数を両方更新する。

### Flags

| Flag | Default | 説明 |
| --- | --- | --- |
| `--project` | cwd から canonical 化 | 対象 project (canonical name または絶対パス) |
| `--harvest-dir` | `$DREAM/harvest` | harvest ログのディレクトリ |
| `--memory-dir` | `~/.claude/projects/<project>/memory` | feedback memory のディレクトリ |
| `--skills-dir` | `~/.claude/skills` | skill 本体のディレクトリ |
| `--date` | 今日 | candidates 出力ファイルの日付 |
| `-v`, `--verbose` | `false` | 処理サマリを stderr に出力 |

## `dream stage`

mine の candidates.jsonl から user 承認 gate 用の Markdown レポートを生成する。LLM 呼び出しなし。

### 出力

cwd の `./tmp/dream/YYYY-MM-DD-<project-slug>.md`。そのセッションで読んで判断するための読み捨ての文書なので、
作業中のプロジェクトの `./tmp` に置く (sandbox の中で書ける場所でもある)。

### レポート内容

- 候補数と kind 内訳 (`skill_body` / `memory_dedup` / `unhomed` の件数)
- 行き先表: 全候補を 1 行ずつ出す。`unhomed` の行き先セルだけ空欄で、決めていない memory が表の上で見える
- 各候補セクション: `id / kind / confidence / coverage_ratio / memory_path / skill_path / evidence_sessions / diagnosis`
- 承認手順 (accept / revise / reject 3 択 + `dream adopt --candidate <id>` 案内)

`proposed_change` の diff 本文は Adopt phase で LLM が生成する設計。Stage レポートには含まれない。

### Flags

| Flag | Default | 説明 |
| --- | --- | --- |
| `--project` | (必須) | 対象 project (canonical name) |
| `--date` | 今日 | candidates 対象日 |
| `--candidates` | auto-resolve | candidates.jsonl のパス |
| `--out` | `./tmp/dream/<date>-<slug>.md` | レポートの出力先 |
| `-v`, `--verbose` | `false` | 処理サマリを stderr に出力 |

## `dream adopt`

candidate ID を指定し、対応する repo で作業ブランチを作成する。実際の SKILL.md への Edit は本 CLI では行わず、
Claude Code (LLM) がインタラクティブに行う。

`kind: unhomed` の candidate は branch を切る前にエラーで拒否する。行き先が決まっていない memory に対して
Edit 対象を勝手に決めないため。dream skill の行き先テーブル
([layer-auto-memory.md](layer-auto-memory.md)) で行き先を決めてから呼ぶ。

### Branch フォーマット

`dream/YYYY-MM-DD-<candidate-slug>`

`candidate-slug` は candidate ID の `dream-candidate-` prefix を剥がしたもの。

### repo 判定

candidate の `skill_path` (memory_dedup は `memory_path`) の symlink 実体を辿り、`git rev-parse --show-toplevel` で
正しい repo root を得る。

git 管理外の path は明示エラーにして手動対応を促す。
auto memory の `~/.claude/projects/<canonical>/memory/*.md` がこれに当たる。

### 出力

stdout へ LLM 向けの手順書を出す。手順書には次の項目が入る。
- kind / confidence / associated_skill
- target file / repo root / 作業ブランチ
- Diagnosis (mine が生成)
- 次に user または LLM がすべき手順 (Edit, diff レビュー, commit)
- memory 側の参考文言 (memory_path 中身を Edit の材料に)

### push は自動しない

user が明示的に指示するまで push しない (「入力非破壊」原則)。

### Flags

| Flag | Default | 説明 |
| --- | --- | --- |
| `--candidate` | (必須) | 対象 candidate ID |
| `--candidates` | auto-resolve | candidates.jsonl のパス |
| `--project` | — | canonical name (candidates auto-resolve に必要) |
| `--date` | 今日 | candidates 対象日 |
| `--dry-run` | `false` | branch 作成を skip し手順だけ表示 |

## `dream forget`

consolidation で役目を終えた auto memory を削除し、`MEMORY.md` の索引行 (`](<name>.md)` を含む行) も同時に除去する。

```sh
~/.cache/agents-daemon/bin/agents-daemon dream forget --dry-run <name>...   # 対象を確認
~/.cache/agents-daemon/bin/agents-daemon dream forget <name>...             # 削除
```

パスは literal で打つ。変数から来る command name は `excludedCommands` に当たっても sandbox に残る。

### なぜ CLI が要るか

`~/.claude/projects/**` は Claude Code 組み込みの sandbox 保護対象で、bash の `rm` は `Operation not permitted` になる。
解決結果の write ルールは `allowOnly` + `denyWithinAllow` の 2 段しかなく、deny された領域を settings.json 側から
再許可する表現が存在しない。そのため `sandbox.excludedCommands` に `dream forget` だけを登録し、削除範囲の検証を
CLI 内部に持たせている。登録する行は `agents-daemon:setup` skill の「dream forget の sandbox 除外」。

### 安全側の制約

- 削除できるのは memory ディレクトリ直下の `.md` のみ。`../` を含む名前は拒否する
- `MEMORY.md` 自体は削除できない
- 1 件でも解決できない名前があれば何も削除しない (索引と実体の乖離を避ける)

### Flags

| Flag | Default | 説明 |
| --- | --- | --- |
| `--project` | cwd | canonical name または絶対パス |
| `--memory-dir` | `~/.claude/projects/<project>/memory` | memory ディレクトリ |
| `--dry-run` | `false` | 削除せず対象だけ表示 |
