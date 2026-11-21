# agents-daemon dream (CLI の実装リファレンス)

`dream` skill が使う Go の実行系。agents-daemon のバイナリの `dream` サブコマンドとして、
`harvest` / `mine` / `stage` / `adopt` / `forget` / `check` / `policy` の 7 つを提供する。
利用者の規約 (config.json の `dream.policy`) の形は
[README の「利用者の設定」](../../../README.md#利用者の設定) にある。

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
| `dream check` | 7 Adopt の後 | 判断の要らない点検 (read-only)。error があれば exit code 1 |
| `dream policy` | (全 phase) | 利用者の規約のファイルを連結して JSON で出す |

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
mtime で消す (`dream` パッケージの `Prune`)。daemon の housekeeping は rex の外では走らないので、
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

### 他の plugin との契約

harvest は他の plugin の skill が索引として読みうるので、次を互換を保つ対象にする。dream の側は読み手を知らない。

- 置き場 `$DREAM/harvest/YYYY-MM-DD.jsonl` と、1 行 1 セッションの JSON Lines であること
- フィールド `session_id`、`cwd_project`、`skills_invoked`、`feedback_signals` (`{quote, kind}`) の名前と形。
  `cwd_project` は transcript の置き場 (`~/.claude/projects/<cwd_project>/`) の名前で、cwd を読めなかった
  セッションでは欠ける
- 90 日の保持 (`dream` パッケージの `retention`)

変える PR は、タイトルに harvest の形が変わることを書く。tagpr がタイトルを CHANGELOG へ写す。
harvest が無い環境 (agents-daemon が入っていない、まだ 1 度も harvest していない) でも動くことは、読む側が保証する。
`user_prompts` は切り詰めてあるので、読む側は索引にだけ使い、中身は `session_id` から transcript を引いて読む。

### PII 伏せ字化ルール

利用者の規約の `pii` の規則を先に、組み込みの規則 (email アドレス → `[EMAIL]`、日本の携帯電話番号 → `[PHONE]`) を
後に当てる。user_prompts、feedback_signals の quote、failed_invocations の user_reaction は、
どれも伏せた後の文字列から作る。
規約が読めなければ、何も書かずにエラーで止まる。

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
- `<dir>/<name>/SKILL.md` (利用者の規約の `skillDirs`。global skill の後に探す)
- `<project_root>/.claude/skills/<name>/SKILL.md` (fallback、project-local skill)

skill 名と memory 名の照合で外す接頭辞は、利用者の規約の `skillNamePrefixes` から受ける。

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

`evidence_sessions` が 1 つだけの候補は、coverage が high / medium でも `low` に落とす。

閾値の spec は [skill-mining-heuristics.md](skill-mining-heuristics.md) を single source of truth とする。
閾値を変えるときは、この spec と `mine/coverage.go` の定数を両方更新する。

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
- 次に user または LLM がすべき手順 (Edit, `dream check`, diff レビュー, commit)
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

## `dream check`

adopt の Edit の後、commit の前に回す機械の点検。read-only で、何も直さない。項目を
`<path>:<line>: <severity>: ...` の形で 1 行ずつ stdout に出し、`error` が 1 件でもあれば exit code 1 で終わる。
`warning` は人が判断する合図で、exit code には効かない。`--json` は `{"findings": [...]}` を出し、exit code は同じ。

| 点検 | kind | severity | 落ちる条件 |
| --- | --- | --- | --- |
| `MEMORY.md` の行数 | `index_too_long` | error | 200 行以上 (SKILL.md のチェックリストの「200 行未満」) |
| memory 1 ファイルの行数 | `memory_too_long` | warning | 100 行超 (SKILL.md のトリガー条件。細分化か剪定かを決める) |
| auto memory の `[[name]]` | `dangling_link` | warning | どの memory の frontmatter の `name` にも当たらない |
| auto memory (本体と `MEMORY.md`) の相対日付 | `relative_date` | error | 昨日・先週・今月などの語がコードの外にある |
| SKILL.md の相対パス | `missing_path` | error | `references/`・`scripts/`・`prompts/` で始まるパスが skill のディレクトリに無い |
| auto memory と SKILL.md の skill 名 | `unknown_skill` | error | inline code の `<plugin>:<name>` が、入っている plugin の `skills/<name>` にも `commands/<name>.md` にも無い |

`[[name]]` はファイル名ではなく frontmatter の `name` で解く。まだ無い memory を指すリンクは Claude Code の
memory の仕様で「後で書く」の印として許されるので、warning に留める。消す memory を指すリンクが残る事故は
`dream forget` が消す前に拾う。fence と inline code の中、空白を含む `[[ ... ]]` (shell の test 構文) は
リンクとして読まない。memory ディレクトリが無ければ 0 件ではなくエラーにする。

点検する skill は `--skills-dir` (既定は `~/.claude/skills`) と利用者の規約の `skillDirs` に置かれたもの。
plugin は Claude Code の `plugins/installed_plugins.json` から引く。

点検しないものと理由:

- CLAUDE.md の行数と文字数: dream skill にも references にも上限の数値が無い
- skill の相対日付: description のトリガーの言い回し (「今月どこで使った」) として正当に使われる
- `${CLAUDE_PLUGIN_ROOT}/scripts/...` とリンクの表示文字列の中のパス: skill のディレクトリから解けない、または参照ではない
- 名前空間の無い skill 名: skill 名かどうかを文字列から判定できない
- 入っていない plugin の `<plugin>:<name>`: plugin の名前か、別の `a:b` の形の語かを判定できない

| Flag | Default | 説明 |
| --- | --- | --- |
| `--project` | cwd から canonical 化 | 対象 project (canonical name または絶対パス) |
| `--memory-dir` | `~/.claude/projects/<project>/memory` | memory ディレクトリ |
| `--skills-dir` | `~/.claude/skills` | SKILL.md を点検する skill の置き場 (規約の `skillDirs` も見る) |

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
- 消す memory を `[[name]]` で指している memory (消す memory 同士は除く) を `<path>:<line>:` の形で先に出す
  (`--json` は `linked_from`)。削除は止めないので、指す側を直すか残すかは出力を見て決める

### Flags

| Flag | Default | 説明 |
| --- | --- | --- |
| `--project` | cwd | canonical name または絶対パス |
| `--memory-dir` | `~/.claude/projects/<project>/memory` | memory ディレクトリ |
| `--dry-run` | `false` | 削除せず対象だけ表示 |

## `dream policy`

config.json の `dream.policy` が指す規約のファイルを配列の順に読み、各フィールドの配列をその順で連結して
JSON で stdout に出す。flag は無く、`--json` の有無で出力は変わらない。
キーが無ければ `{}`。読めない・壊れている・未知のフィールドを含む規約があればエラーで止まる。
`dream harvest` と `dream mine` も同じ読み方をするので、規約を直したらこのコマンドで確かめる。
