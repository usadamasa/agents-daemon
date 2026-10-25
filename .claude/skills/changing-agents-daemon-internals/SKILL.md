---
name: changing-agents-daemon-internals
description: >-
  agents-daemon の実装 (statusline と daemon のファイル越しの配線、利用上限の検知と待機、pane の読み取りと送信、
  rex の backend、ボードとパーキングの mod、バイナリの用意と差し替え) を変える・不具合の原因を切り分ける・
  実機で検証するときに使う。
---

# agents-daemon の内部

agents-daemon の仕組みと、その仕組みを選んだ理由の置き場。利用者が打つコマンド・設定キー・既知の制約は
plugin の `agents-daemon:agents-daemon` skill の `references/operations.md` が持ち、ここには写さない。

## references

`Read` で開く。

| ファイル | 中身 |
| ---- | ---- |
| [architecture.md](references/architecture.md) | 部品の連動と出力されるファイル |
| [statusline-state.md](references/statusline-state.md) | statusline が書く state |
| [rate-limit.md](references/rate-limit.md) | 利用上限の検知と待機 |
| [pane-io.md](references/pane-io.md) | pane の読み取りと送信 |
| [program-status.md](references/program-status.md) | rex の program_status (OSC 7501) |
| [multiplexer.md](references/multiplexer.md) | 多重化ツールの backend (rex) |
| [board.md](references/board.md) | ボードとパーキング (mod) |
| [binary.md](references/binary.md) | バイナリの用意と差し替え |
| [verification.md](references/verification.md) | 検証用コマンドと実機での検証 |

## 症状から引くところ

| 症状 | 見るところ |
| ---- | ---- |
| 上限が解除されても再開しない | rate-limit.md の「待機時刻の決め方」「ゲート」 |
| 関係ない pane にプロンプトが打ち込まれた | rate-limit.md の「limit の判定は 2 つの手がかりを要求する」「ゲート」 |
| ネイティブの auto-continue と取り合う、解除後に Enter だけ送られる | rate-limit.md の「Claude Code 自身の auto-continue との関係」 |
| `doctor` の pane 一覧が全部 `agent_status=unknown` (rex を上げた直後) | program-status.md の「record を使う条件」 |
| daemon が起きない・バイナリがビルドされない | binary.md の「build」 |
| 直したコードが動いていない | binary.md の「動いている daemon の入れ替え」 |
| 送信が block に届かない | pane-io.md の「送信」 |
| `doctor` の pane 一覧が `session_id=(なし)` になる | statusline-state.md の「書式」(`context/<sid>.json` の `multiplexer`) |
| rex のセッションなのに daemon が起きない | multiplexer.md (`REX_SESSION` の無い起動では起こさない) |
| 検知の挙動を確かめたい、上限を踏んだ後に材料を読みたい | verification.md |
