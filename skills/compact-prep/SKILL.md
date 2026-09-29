---
name: compact-prep
description: |
  Claude Code の /compact 実行前に､現セッションの作業状態を一時 state file へ保存する｡
  MANDATORY TRIGGERS: /compact-prep, /agents-daemon:compact-prep, compact-prep, 圧縮準備, compact 準備, コンパクト準備｡
  DO NOT TRIGGER: /compact 直後の runtime 復旧処理､plan 作成､compact 直後の再開処理｡
strict_procedure: true
argument-hint: "[復旧するか]"
allowed-tools: Read, Write, Bash
---

# compact-prep

Claude Code の `/compact` 実行前に､圧縮サマリーでは失われる作業状態を
`${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/compact-state/<SESSION_ID>.md` に保存する｡
`/compact` の LLM 要約は「あらすじ」で本文の細部 (どのフェーズか / どのファイルを触っていたか / 何が未確認か)
は落ちるため､再開時の復旧材料として構造化 state を別途書き出す｡

この skill が state file を書くことが､agents-daemon の daemon にとって
「compact-prep が走った」の唯一の判定材料になる (ファイルの mtime で見る)｡
`/compact` は daemon が続けて投入するので､この skill から実行する必要は無い｡

## Strict procedure profile

- Strictness: strict-procedure｡state file の見出し構造と保存完了報告の一致を左右する｡
- Hard gates: session_id が取得できない場合は state file を作らず､その旨をユーザーに伝えて終了する｡
- Forcing function: 保存先パス・保存内容・未確認事項を明示する｡
- Completion receipt: state file パス､保存した主要項目､未確認項目､次に取りたいアクションを提示する｡

## 手順

1. **session_id の取得**
   - `"${CLAUDE_PLUGIN_ROOT}/scripts/get-session-id.sh"` を実行する｡
   - 取得できない場合は state file を作らずに「session_id が取得できないため中断」と報告して終了する (Hard gate)｡
2. **保存先の決定**
   - `${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/compact-state/<SESSION_ID>.md` を保存先とする｡
   - hook と daemon も同じディレクトリを見る (drift 防止は plugin の `hooks/lib/compact-markers.sh` の定数と
     daemon 側の `internal/apppath` の対で担保)｡
   - `pwd` からは決めない｡daemon は別プロセスで､hook に渡る cwd も `cd` や worktree で動くため､書く場所と読む場所が食い違う｡
   - 親ディレクトリが無ければ `mkdir -p` で作成する｡
3. **収集する情報**: 下記「保存内容の詳細」の全項目を集める (active plan は `~/.claude/plans/` 配下､TaskList は `TaskList` ツールから)｡
4. **state file への書き込み** — 以下の見出しをこの順で必ず含める｡
   - `# Compact Prep State`
   - `## Active Plan`
   - `## Current Phase`
   - `## TaskList Summary`
   - `## Session Decisions`
   - `## Constraints and Blockers`
   - `## Worker Topology`
   - `## Editing Files`
   - `## Recovery Notes`
5. **検証**: 保存後に state file を Read し､上記 9 見出しが全て存在することを確認する｡
6. **Completion receipt**: 下記フォーマットでユーザーに伝える｡

## 保存内容の詳細

- **Active Plan**: plan file の絶対パスと､現在のフェーズ／ステップ番号｡
- **Current Phase**: plan の中で現在進行中のフェーズ (無ければ「フェーズ管理無し」)｡
- **TaskList Summary**: in-progress タスク一覧と､各タスクの補足 (採用したい／却下した理由)｡
- **Session Decisions**: 会話で合意した設計判断｡採用と却下の両方を残す｡
- **Constraints and Blockers**: 外部依存・未解決の質問・調査中の項目｡
- **Worker Topology**: tmux-bridge 使用時は pane / role / 担当を記録｡未使用なら「未使用」｡
- **Editing Files**: 未保存または注意が必要な編集中ファイルの絶対パスと､注意点｡
- **Recovery Notes**: 再開時に最初に読むべきファイル､次のアクション､再開手順｡

## Completion receipt (必須フォーマット)

```
✅ compact-prep 完了

- state file: <path>
- 保存した主要項目: <list>
- 未確認項目: <list>
- 次のアクション: 「/compact の投入を待ちます (daemon が state file を検知して自動で打ちます)｡」
```

daemon が動いていない環境ではユーザーが自分で `/compact` を打つことになる｡
その場合も報告の形は変えず､待ちであることだけ伝える｡

## Do not

- `/compact` を skill 内で実行しない (投入は agents-daemon の daemon が行う)｡
- state file を上書きする際に既存内容を破棄しない場合でも､必ず「上書き前の内容を確認したか」を提示する｡
- session_id が取れない状態で先に進まない (Hard gate)｡

## Marker protocol (参考)

本 skill は `${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/` 配下の 2 種類の state を介して､hook 2 本と
agents-daemon の daemon と協調している｡path 文字列は plugin の `hooks/lib/compact-markers.sh` の
定数 (hook / skill 側) と `internal/apppath` (daemon 側) が対で管理する｡

cwd 相対 (`./tmp` 配下) にしないのは､daemon が別プロセスであり､hook に渡る cwd も
Claude が `cd` したり worktree に入ると動くため｡`$TMPDIR` も使えない (sandboxed な skill の
Write と excluded で起動する hook の shell とで解決値が食い違う)｡

| State | 書く側 | 消す側 | 意味 |
| ---- | ---- | ---- | ---- |
| `compact-state/<sid>.md` | 本 skill (`/agents-daemon:compact-prep`) | daemon の housekeeping (7 日) | 復旧用の state file 本体｡mtime が「prep が走った」の判定 |
| `compacted/<sid>` | `hooks/compaction-recovery.sh` (PostCompact) | `hooks/userpromptsubmit-compaction-recovery.sh` (復旧ガイド注入時) | 圧縮完了 marker｡復旧トリガーと daemon の再開判定を兼ねる |
| `context/<sid>.json` | `agents-daemon ingest-statusline` (利用者の statusline が毎描画で呼ぶ) | daemon の housekeeping (7 日) | context 使用率と window の大きさ｡閾値の compact は使用率を､idle compact は window の大きさを使う |

サイクル: 使用率が閾値超え → daemon が `/agents-daemon:compact-prep` を投入 → 本 skill が `compact-state` を書く
→ daemon が `/compact` を投入 → PostCompact hook が `compacted` を書く → daemon が再開を促す
→ UserPromptSubmit で復旧ガイドが注入され `compacted` が消える｡
