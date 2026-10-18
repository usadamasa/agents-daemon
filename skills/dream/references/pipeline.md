# 実行 pipeline と実施モデル

dream の実行系。どのレイヤーを触るかではなく、どの順に何を走らせるかを扱う。

## agents-daemon dream の起動

実行系は agents-daemon plugin のバイナリの `dream` サブコマンド群。バイナリは PATH に載らないので絶対パスで呼ぶ。
置き場は `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` で、SessionStart hook が建てる
(無ければ `agents-daemon:setup` skill の「バイナリ」の段)。

```sh
~/.cache/agents-daemon/bin/agents-daemon dream harvest --incremental
~/.cache/agents-daemon/bin/agents-daemon dream mine --project -Users-x-src-repo
~/.cache/agents-daemon/bin/agents-daemon dream stage --project -Users-x-src-repo
```

`XDG_CACHE_HOME` を設定している環境ではその配下に読み替える。各 subcommand の flag は `--help` を参照。
以下の表と references では `agents-daemon dream <phase>` と略記する。

state は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/` に置く (`harvest/`、`candidates/`、
Replay と Gate が書く `replays/`、`verdicts/`)。90 日より古いファイルは `harvest` が消す。

`dream forget` は sandbox の保護下にある auto memory を消すので、settings の `sandbox.excludedCommands` に
登録して sandbox の外で動かす。登録する行と確かめ方は `agents-daemon:setup` skill の「dream forget の sandbox 除外」。
他の phase は sandbox の中で動く (`stage` は cwd の `./tmp/dream/` に書く)。

出力 schema・PII 規則・repo 判定などの実装詳細は [cli.md](cli.md) にある。

## 7-phase pipeline

| Phase | 実行方法 | 手動実行コマンド |
|-------|---------|--------------|
| 1 Harvest | Stop hook で自動 (plugin の `hooks/dream-harvest.sh`) | `agents-daemon dream harvest` |
| 2 Mine | Go CLI (grep-based coverage) | `agents-daemon dream mine` |
| 3 Replay | LLM subagent ([layer-skills](layer-skills.md)) | — |
| 4 Consolidate | 手動 (SKILL.md の「進め方」) | memory 削除のみ `agents-daemon dream forget` |
| 5 Gate | LLM 3-judge majority vote ([layer-skills](layer-skills.md)) | — |
| 6 Stage | Go CLI (Markdown 承認レポート、`./tmp/dream/<date>-<slug>.md`) | `agents-daemon dream stage` |
| 7 Adopt | Go CLI (branch 作成 + LLM 向け指示文) | `agents-daemon dream adopt` |

**ワンパス実行**: Harvest → Mine → Stage を順に実行する。Adopt は candidate 単位で個別に実行する。

`agents-daemon dream adopt` は `kind: unhomed` の candidate を拒否する。行き先を決めてから呼ぶ
([layer-auto-memory](layer-auto-memory.md))。

## 本環境での実施モデル

Anthropic Dreams API は入力 store を非破壊で扱い、出力を別 store に書いて、opt-in のレビューを経て採用する。
Claude Code には Managed Agents API が無いため、**論理単位ごとの commit** と **ユーザーレビュー** で代替する。

- 入力非破壊 ≒ 後で revert できる状態を保つ
  - グローバル設定や skill が git 管理下にあるなら (例: `~/.claude/` を dotfiles リポジトリへ
    symlink している場合)、その実体パスのリポジトリで commit 単位に分ける
  - git 管理外なら、編集前に該当ファイルをコピーしてバックアップを取る (`cp foo.md foo.md.bak`)
- opt-in 採用 ≒ push (および symlink 経由の反映) は明示指示まで保留し、ユーザーが diff を見て判断する
- 出力鵜呑み禁止 ≒ dream で生成した記述に hallucination が混入し得るので、削除も追記も必ず
  diff レビューを通す

## 実体パスの解決

1. 編集対象が `~/.claude/CLAUDE.md` や `~/.claude/skills/<name>/SKILL.md` の場合、`readlink` で
   symlink 先を辿り、実体ファイルを編集する。
2. 実体が git 管理下のリポジトリにあれば、そのリポジトリで作業ブランチを切って編集し commit する。
   worktree 環境なら `git rev-parse --git-dir` と `--git-common-dir` を比較して、編集先パスが
   想定どおりか確認する。
3. 実体が git 管理外なら、編集前にバックアップを取り、変更後に diff をユーザーへ提示する。
