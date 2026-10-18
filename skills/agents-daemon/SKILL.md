---
name: agents-daemon
description: >-
  agents-daemon plugin (Claude Code のセッションのそばに常駐し、セッションの中からはできないことを背後で担う
  デーモン) の運用と切り分けに使う。利用上限からの自動再開が期待どおり動かない、daemon やバイナリが動いていない
  疑いがある、statusline / hook / daemon のファイル越しの配線や設定キーを確かめたい、といったときに引く。
---

# agents-daemon

agents-daemon の運用と切り分けの手引き。役割の区分と「本体ができることは本体に任せる」境界は plugin root の
`README.md` の「役割」、部品のファイル越しの配線は [architecture.md](references/architecture.md)、
コマンドと設定キーは [operations.md](references/operations.md) が持つ。

## 症状から引くところ

<!-- cc-premise: 2.1.296 hooks claude --debug が hook ごとの所要時間を Hooks: 行で出す -->

| 症状 | 見るところ |
| ---- | ---- |
| 上限が解除されても再開しない | rate-limit.md の「待機時刻の決め方」「ゲート」 |
| 関係ない pane にプロンプトが打ち込まれた | rate-limit.md の「limit の判定は 2 つの手がかりを要求する」「ゲート」 |
| ネイティブの auto-continue と取り合う、解除後に Enter だけ送られる | rate-limit.md の「Claude Code 自身の auto-continue との関係」 |
| `doctor` の pane 一覧が全部 `agent_status=unknown`、daemon.log に `agent_status を判定できません` が出る (rex を上げた直後) | program-status.md の「record を使う条件」。rex の record の形が変わった疑い |
| daemon が起きない・バイナリがビルドされない | architecture.md の「バイナリの用意と差し替え」、`build.log` |
| 直したコードが動いていない、plugin を更新したのに古い daemon が動いている | architecture.md の「バイナリの用意と差し替え」、operations.md の「plugin の更新後に daemon を入れ替える」 |
| 送信が block に届かない | pane-io.md の「送信」、operations.md の「既知の制約」(block 内 tmux、rex 不在) |
| `doctor` の pane 一覧が `session_id=(なし)` になる | architecture.md の「statusline が書く state」(`context/<sid>.json` の `multiplexer`) |
| rex のセッションなのに daemon が起きない | architecture.md の「多重化ツールの backend」(`REX_SESSION` の無い起動では起こさない)。`doctor` の daemon の節で起動中の pid を見る |
| `stop` が `operation not permitted` で弾かれる | operations.md の「既知の制約」 |
| 放置した worktree が回収されない、今すぐ回収したい、`prune-worktrees` が `operation not permitted` で落ちる | operations.md の「放置された worktree を回収する」 |
| hook が 10 秒の timeout に当たっている疑い (prompt の送信が遅い、hook の効果が出ない) | `claude --debug` で起動し、`Hooks: SessionStart [...] finished with status 0 (31ms)` の行 (Claude Code 2.1.296+) で hook ごとの所要時間を見る。`timed out after <N>ms` なら当たっている |
| 圧縮後に作業が再開しない、離席中に context が圧縮された | daemon の役目ではない。Claude Code 本体の autocompact / idle compaction (`/config` の `Idle compaction`) の挙動 |

表に無い症状の置き場: install 直後の前提の確認と statusline / peer messaging の配線は `agents-daemon:setup` skill、
`dream harvest` (Stop hook) と記憶の consolidation は `agents-daemon:dream` skill、
peer への送受信は `agents-daemon:peer-sessions` skill。

## references

`Read` で開く。

| ファイル | 中身 |
| ---- | ---- |
| [architecture.md](references/architecture.md) | statusline / hook / daemon の連動、バイナリの用意と差し替え、prune-worktrees の配線、多重化ツールの backend (rex)、毎 tick の判定表、出力されるファイル、ログの rotate |
| [rate-limit.md](references/rate-limit.md) | ネイティブ auto-continue との分担、limit 検知の二重シグナルとゲート、待機時刻の決め方 |
| [pane-io.md](references/pane-io.md) | 画面の読み取り元、送信のキーストロークと文面の制約 |
| [program-status.md](references/program-status.md) | rex の `program_status` (OSC 7501) と `agent_status` の対応、record を使う条件 (owner.pid と foreground の pid の突き合わせ)、payload の書式 |
| [operations.md](references/operations.md) | コマンドと `--dry-run`、plugin の更新後の入れ替え、放置 worktree の回収、実機での検証順序、設定キー一覧、既知の制約 |
