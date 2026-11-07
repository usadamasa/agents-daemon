---
name: triaging-issues
description: >-
  agents-daemon の issue と PR に付ける area ラベルを選ぶとき、機能を足したり廃止したりして area の表を見直すときに使う。
  ラベルの軸と付け方の手順は labeling-github-issues skill の担当。
---

# agents-daemon の area

ラベルの軸、種類の決め方、付け方、PR への写し方は labeling-github-issues skill に従う。
この skill は agents-daemon 固有の area の表と、area を作らない廃止した機能を持つ。

## area の表

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

ボードと pane の issue は、見た目が変わっても機能が続いているので `area: deps-graph` に入る。

## 廃止した機能

次の機能の issue には area を付けない。

- herdr
- daemon の idle compact
- ttl-guard
- prompt cache の TTL の表示と ack
- PreModelSwitch
- domain (私用と業務の境界)
