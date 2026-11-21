# agents-daemon の全体構成と出力ファイル

## 全体の連動

statusline・hook・daemon が別々のタイミングで動き、ファイルを介して繋がる。互いを直接呼ばない。
どれかが止まっていても、ほかは前回のファイルを読んで動き続ける。

```
[1] Claude Code ──再実行──stdin──> agents-daemon statusline (statusLine.command)
                                   または 利用者の statusline ──stdin──> agents-daemon ingest-statusline
                                                   ├──> context/<session_id>.json     (毎回。block → session の対応)
                                                   ├──> rate-limits/<session_id>.json (あれば。doctor が出すだけ)
                                                   └──> cost/<YYYY-MM-DD>/<session_id>.json (statusline だけ、上限の env があるとき)
                                                                          │
[2] SessionStart hook ──daemon --ensure──> daemon (常駐 1 プロセス)        │ pane の
                                              │                           │ session を
                                              │ 毎 tick (既定 5 秒) <──────┘ 引く (無ければ登録簿の pid)
                                              │
                                              ├─ PaneList ────────────> Claude の pane 一覧 (record の agent_status を含む)
                                              ├─ transcript の末尾 ───> ~/.claude/projects/*/<session_id>.jsonl
                                              │  (ここで上限かどうかを判定)
                                              │
[3]                                           └─ 上限解除の時刻まで待つ
                                                 ├─ PaneRead ──────────> 待機明けに画面の通知欄を読んでキーを選ぶ
                                                 └─ SendText / SendKeys で再開 (backend は rex)
```

| 段 | 実体 |
| ---- | ---- |
| [1] statusline が block と session の対応を渡す | `sessionstate`、`sessioncontext`、`ratelimit`。記録の無い pane は `claudesessions` が登録簿で補う |
| [2] SessionStart hook が daemon を起こす | `hooks/ensure-daemon.sh`、`daemonstate` (起動の排他)。バイナリの用意は `hooks/build-daemon.sh`、動いている daemon の入れ替わりは `daemon/selfupdate.go`、`update` は `selfupdate` |
| [3] daemon が監視して再開する | `monitor`、`detect`、`transcript`。毎 tick の判定の順は `monitor` の package doc、不変条件は `monitor/invariants_test.go` |
| pane の列挙・読み取り・送信 | `multiplexer` (呼び手ごとの interface)、`backend` (rex の外では起こさない)、`rex` (操作と method の対応)、`rex/watch` と `rex/blockstate` (購読の cache)。daemon は rex を control API だけで操作し、CLI を叩くのは spawn の包み (`rex/wrap`) だけ |
| 依存図とパーキングロット | `deps`、`deps/graph`、`edge`、`ledger`、`tell`、`parking`、`parkhand`。mod は `hooks/register.ts` と各 `hooks/*.ts`、アプリは `gui/` の冒頭 |
| CLI の組み立て | `cmd/agents-daemon/internal/*cli` の package doc。cobra は `cmd/` の下だけ (`.go-arch-lint.yml`) |

daemon が担わないもの:

- context の compact と prompt cache の失効。Claude Code 本体の autocompact・idle compaction と、失効後の footer の案内に任せる
- 放置された worktree の回収 (`prune-worktrees`) は SessionStart hook が行う。daemon は rex の外では起きず、対象リポジトリの
  一覧も cwd も持たないため。subagent の worktree は本体の periodic sweep が消すので触らない (`worktree` の package doc)

## 出力されるファイル

設定は利用者が書くものなので XDG の config ディレクトリに置く。ランタイム状態は揮発物なので
ハーネスの `~/.claude/` に混ぜず、XDG の state ディレクトリ `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`
(以下 `$STATE`) に置く。環境から埋める入口は `apppath.FromEnv` だけ。

この表はファイルを書く主体と消す主体の対応を 1 枚で引くための索引。中身と書く時機は実体のパッケージの doc が持つ。
保持期間の値と根拠は実体のパッケージの定数が持ち、依存図とパーキングロットが読む記録の保持期間の間の関係は
`daemon/retention_test.go` が固定する。値か関係を変えるときは、そのテストとこの表を一緒に直す。

| パス | 書く主体 | 消す主体 (保持期間) | 実体 |
| ---- | ---- | ---- | ---- |
| `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` | 人間 | 消さない | `config` |
| `$STATE/rate-limits/<session_id>.json` | `statusline`、`ingest-statusline` | daemon (24 時間) | `ratelimit` |
| `$STATE/context/<session_id>.json` | `statusline`、`ingest-statusline` | daemon (7 日) | `sessioncontext` |
| `$STATE/cost/<YYYY-MM-DD>/<session_id>.json` | `statusline` (`CLAUDE_STATUSLINE_SPEND_CAP` があるとき) | `statusline` (45 日) | `statusline/spend` |
| `$STATE/tasks/<session_id>/<task_id>.json`、`…/todos.json` | `ingest-task` (PostToolUse hook) | daemon (完了 1 日、ほか 7 日)、`park adopt`、`task dismiss` | `tasklog` |
| `$STATE/work/<session_id>.json`、`…/.lock` | `ingest-work` (hook)、`work set` | daemon (7 日)、`park adopt`、`task dismiss`。`.lock` は消さない | `worklog` |
| `$STATE/parking/<id>.json`、`…/.lock` | `park add` / `set` / `discard` / `withdraw` / `adopt` | daemon (open 90 日、handed / discarded 7 日)。`.lock` は消さない | `parking` |
| `$STATE/edges/<id>.json`、`…/.lock` | `edge add` / `rm`、daemon (端点の結びつけ、PR への辺) | daemon (7 日、済んだ参照の辺)、`edge rm`、`task dismiss`。`.lock` は消さない | `edge`、`refpoll` |
| `$STATE/edges/<id>.declined` | `edge rm` (`sid:` → `pr:` の辺) | daemon (7 日)、`edge add` | `edge` |
| `$STATE/sessions/<session_id>.json` | daemon | daemon (7 日) | `ledger` |
| `$STATE/spawned-sessions/<id>.json` | `spawned-session mark` (spawn の包み) | daemon の reaper、`spawned-session close` (古さでは消さない) | `spawnedsession` |
| `$STATE/refs/<id>.json` | daemon | daemon (7 日) | `refstate` |
| `$STATE/daemon.lock` | daemon | 消さない | `daemonstate` |
| `$STATE/daemon.pid` | daemon | daemon、`stop` | `daemonstate` |
| `$STATE/status.json` | daemon | 上書き | `daemonstate` |
| `$STATE/logs/daemon.log`、`…/events.log` | daemon、`eventlog` を使う CLI と mod | rotate | `applog`、`eventlog` |
| `$STATE/logs/*.log.YYYY-MM-DD` | rotate | daemon (7 日) | `applog` |
| `$STATE/limit-events/<YYYY-MM-DD>.jsonl` | daemon | daemon (30 日) | `daemon/limitlog` |
| `$STATE/turn-failures/<session_id>.json` | `ingest-turn-failure` (hook) | 次の turn の hook、daemon (24 時間) | `turnfailure` |
| `$STATE/quota-resume/<session_id>.json` | `ingest-quota-resume` (hook) | daemon (24 時間) | `quotaresume` |
| `$STATE/logs/build.log` | `hooks/build-daemon.sh`、`hooks/build-gui.sh` | 消さない | `hooks/lib/go-build.sh` |
| `$STATE/build.failed`、`$STATE/build-gui.failed` | 同上 | 同じ script が建てられたとき | `hooks/lib/go-build.sh` |
| `$STATE/logs/dream-harvest.log` | `dream harvest` (Stop hook) | 消さない | `hooks/dream-harvest.sh` |
| `$STATE/dream/harvest/<YYYY-MM-DD>.jsonl`、`…/candidates/`、`…/replays/`、`…/verdicts/` | `dream harvest`、`dream mine`、`agents-daemon:dream` skill | `dream harvest` (90 日) | `dream` |
| `$STATE/dream/harvest-state.json` | `dream harvest` | 上書き | `dream` |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`、`….version` | `hooks/build-daemon.sh`、`task install` | 消さない | `hooks/lib/daemon-bin.sh` |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agent-tasks`、`….version` | `hooks/build-gui.sh` | 消さない | `hooks/build-gui.sh` |
| `~/Applications/agent-tasks.app` | `hooks/build-gui.sh` | 消さない | `hooks/build-gui.sh`、`gui/upgrade.go` |
| `$STATE/gui/restore.json` | agent-tasks (入れ替わる直前) | 次に起きた agent-tasks (読んで消す) | `gui/upgrade.go` |

`$STATE` はデーモンが必要に応じて作る。消しても壊れない (次回の起動と次回の statusline 描画で再生成される)。

静かな tick は**何も書かない**。ログが伸びていないことは、何も起きていないことを意味する。
