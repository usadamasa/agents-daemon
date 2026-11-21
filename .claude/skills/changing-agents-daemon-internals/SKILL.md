---
name: changing-agents-daemon-internals
description: >-
  agents-daemon の実装 (statusline と daemon のファイル越しの配線、利用上限の検知と待機、pane の読み取りと送信、
  rex の backend、依存図とパーキングロットの mod、バイナリの用意と差し替え) を変える・不具合の原因を切り分ける・
  実機で検証するときに使う。
---

# agents-daemon の内部

コードから復元できないもの (パッケージをまたぐ境界とその理由、実測した Claude Code と rex の前提、実機の検証手順) と、
症状から見る場所の置き場。パッケージの中で閉じる仕組みは package doc と `hooks/*.ts` の冒頭が持ち、ここには写さない。
利用者が打つコマンド・設定キー・既知の制約は
plugin の `agents-daemon:agents-daemon` skill の `references/operations.md` が持ち、ここには写さない。

## references

`Read` で開く。

| ファイル | 中身 |
| ---- | ---- |
| [architecture.md](references/architecture.md) | 部品の連動と出力されるファイル |
| [program-status.md](references/program-status.md) | Claude Code の record と画面のモードの実測 |
| [multiplexer.md](references/multiplexer.md) | 多重化ツールの backend (rex) |
| [deps.md](references/deps.md) | 依存図 (JSON、アプリ、tell、辺の通知の mod) |
| [parking.md](references/parking.md) | パーキングロット (メモの手渡し、park tool、通知の mod) |
| [binary.md](references/binary.md) | バイナリの用意と差し替え |
| [verification.md](references/verification.md) | 検証用コマンドと実機での検証 |
| [package-lint.md](references/package-lint.md) | パッケージ構造の lint (analyze-modularity、go-arch-lint) と改名 |

## 症状から引くところ

| 症状 | 見るところ |
| ---- | ---- |
| 上限が解除されても再開しない | `monitor` の package doc と `monitor.Decide`、`monitor.ComputeLimitWake`。`doctor` の pane 一覧の「daemon の扱い」と `inspect --pane` の `plan` |
| 関係ない pane にプロンプトが打ち込まれた | `detect` の package doc (上限は transcript で決める)。limit-events の `transcript` と `hint` (verification.md の「次に上限を踏んだとき」) |
| 週の上限で止まった pane が再開しない | `monitor` の `maxWeeklyWait` (解除が 24 時間より先なら `weekly-cap`) |
| 何日も前に止まった pane に再開の文面が送られた、送られない | `monitor` の `maxTurnAge` (`stale-turn`) |
| ネイティブの auto-continue と取り合う、解除後に Enter だけ送られる | `detect.ClassifyScreen` と `detect` のパターンのテスト、`monitor.ResolveHint`、`config` の `DeferToNativeAutoContinue` |
| `doctor` の pane 一覧が全部 `agent_status=unknown` (rex を上げた直後) | `rex/blockstate` の `ToPane`。状況ごとの record は program-status.md |
| daemon が起きない・バイナリがビルドされない | binary.md の「build」 |
| 直したコードが動いていない | binary.md の「動いている daemon の入れ替え」 |
| 送信が block に届かない | multiplexer.md の対応表 (SendText、SendKeys)。送る順と間隔は `monitor` の再開の送信と `config` |
| `doctor` の pane 一覧が `session_id=(なし)` になる (上限を判定しない) | `sessioncontext` (`context/<sid>.json` の `multiplexer`) と `claudesessions` (登録簿での補い) |
| `doctor` の pane 一覧に `⚠ transcript を読めない` が出る | `transcript` の package doc (置き場と entry の形) |
| rex のセッションなのに daemon が起きない | multiplexer.md (`REX_SESSION` の無い起動では起こさない) |
| 検知の挙動を確かめたい、上限を踏んだ後に材料を読みたい | verification.md |
| `task lint` の analyze-modularity や go-arch-lint が落ちる、パッケージを統合・改名したい | package-lint.md |
