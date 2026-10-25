# agents-daemon の全体構成と出力ファイル

## 全体の連動

statusline・hook・daemon が別々のタイミングで動き、ファイルを介して繋がる。
互いを直接呼ばない。

```
[1] Claude Code ──再実行──stdin──> agents-daemon statusline (statusLine.command)
                                   または 利用者の statusline ──stdin──> agents-daemon ingest-statusline
                                                   ├──> rate-limits/<session_id>.json (あれば)
                                                   ├──> context/<session_id>.json     (毎回)
                                                   └──> cost/<YYYY-MM-DD>/<session_id>.json (statusline だけ、上限の env があるとき)
                                                                          │
[2] SessionStart hook ──daemon --ensure──> daemon (常駐 1 プロセス)        │ pane の
                                              │                           │ session を
                                              │ 毎 tick (既定 5 秒) <──────┘ 引いて読む
                                              │
                                              ├─ PaneList ────────────> Claude の pane 一覧
                                              ├─ PaneRead ────────────> 各 pane の画面テキスト
                                              │  (backend は rex。multiplexer.md)
                                              │
                                              │  ここで判定 (下の表)
                                              │
[3]                                           └─ 上限解除の時刻まで待つ
                                                 └─ SendText / SendKeys で再開
```

context の compact と prompt cache の失効には関わらない。Claude Code 本体の autocompact・idle compaction と、
失効後の footer の案内 (`~222k uncached · /clear to start fresh`) に任せる。

### [1] statusline が上限情報を渡し、バイナリが落とす

利用上限の使用率と解除時刻 (`rate_limits.five_hour.resets_at`、epoch 秒) は statusline の stdin にしか来ない。
`statusLine.command` に向けたバイナリの `statusline` か、利用者の statusline から stdin を受けた
`ingest-statusline` が、`rate-limits/<session_id>.json` (あるときだけ) と `context/<session_id>.json` (毎回) に落とす。
画面の解除時刻の表記をパースせずに済むのがこのツールの中核。stdin の中身、state file の出し分け、書式は
[statusline-state.md](statusline-state.md)。

### [2] SessionStart hook がデーモンを起こす

`hooks/ensure-daemon.sh` が `agents-daemon daemon --ensure` を叩く。生きているデーモンが既にあれば
即座に戻り、無ければ自分自身を detach 起動して戻る。hook を絶対にブロックしない。

「生きているデーモンがあるか」は `$STATE/daemon.lock` の `flock(LOCK_EX|LOCK_NB)` で見る。lock はデーモンが
寿命の間 fd を開いたまま持ち、取れた 1 つの process だけが daemon として進む (取れなければ `ErrAlreadyRunning`)。
`--ensure` は lock を試して、取れなければ何もせず、取れたら離してから detach 起動し、子が取り直す。
持ち主が死ねば OS が解放するので、stale な判定は残らない。pid file (`daemon.pid`) は lock を取った後に書き、
`status` / `doctor` / `stop` が pid を読む用途にだけ使う。

**配線は plugin の `hooks/hooks.json` にある。** 同じファイルの Stop に `hooks/dream-harvest.sh` があり、
同じバイナリの `dream harvest --incremental --detach` を exec する (バイナリが無ければ何もしない)。
こちらも detach して即座に戻り、子の stderr は `$STATE/logs/dream-harvest.log` に残る。
`dream` の役目と state は `agents-daemon:dream` skill の `references/cli.md`。

SessionStart には `hooks/prune-worktrees.sh` も並び、`prune-worktrees --hook` に hook 入力をそのまま渡す。
回収したときだけ `systemMessage` で知らせ、state は書かない。判定の条件の実体は `internal/worktree` の package doc、
手で叩く形は [operations.md](../../../../skills/agents-daemon/references/operations.md) の
「放置された worktree を回収する」。
<!-- cc-premise: 2.1.310 worktrees 本体の sweep は subagent / background session の worktree だけを消し、foreground の keep は残す -->
subagent の worktree (`claude agent` の lock) は Claude Code 本体の periodic sweep (`cleanupPeriodDays`) が消すので
触らない。daemon の housekeeping にしなかったのは、daemon が rex の外では起きず、対象リポジトリの一覧も cwd も
持たないため (#97)。

plugin は mod も持つ。依存のボード (B)、band (A)、パーキングのメモ、park tool は [board.md](board.md)。

バイナリ自体も SessionStart hook が用意し、差し替わったら動いている daemon が自分で入れ替わる。build の条件、
lock、入れ替わりは [binary.md](binary.md)。

### [3] デーモンが監視して再開する

単一プロセスが全 Claude pane を見る。pane ごとの状態は pane ID (rex の block ID) をキーに
プロセス内で持つ。pane の列挙・読み取り・送信は多重化ツールの backend (rex) 越しに行う。
backend の判定と rex の呼び方は [multiplexer.md](multiplexer.md)。

## CLI の組み立て

`cmd/agents-daemon` は `main` と root の登録だけを持つ。コマンドの組み立て (flag、説明文、環境を `internal/` の
本体へ渡す配線) は機能ごとの `cmd/agents-daemon/internal/<family>cli` が持ち、公開するのは root に載せる
`Commands() []*cobra.Command` の 1 つだけにする。判定や組み立ての規則は `internal/` の本体に置き、cli パッケージは
引数を読んで呼ぶだけにする。

| family | コマンド |
| ---- | ---- |
| `boardcli` | park、edge、ingest-task (ボードに載る記録を書く・読む)、board-log (ボードの経路のログに mod の出来事を書く) |
| `daemoncli` | daemon、stop、status、logs、doctor、inspect、simulate (daemon のライフサイクルと読むだけの診断) |
| `dreamcli` | dream |
| `statuslinecli` | statusline、ingest-statusline |
| `worktreecli` | prune-worktrees |
| `jsonflag` | family ではなく root のグローバルオプション `--json` の配線。対応はコマンドが `Annotations` で宣言し、宣言の無いコマンドに付いたら RunE の前に error にする |

cobra を import するのは `cmd/` の下だけで、`internal/` の本体は CLI を知らない。Go の internal 規則で
`internal/` からは cli パッケージを import できず、`.go-arch-lint.yml` は cobra を `cmd_main` (`cmd/**`) にだけ許す。
コマンドを足すときは、呼ぶ本体に合う family に置き、合うものが無ければ family を足して root に 1 行登録する。

## 毎 tick の判定

pane ごとに次を評価する。上限側の詳細は [rate-limit.md](rate-limit.md)、
画面の読み取りと送信の作法は [pane-io.md](pane-io.md)。

| 条件 | 動作 |
| ---- | ---- |
| 待機中で、まだ待機期限に達していない | 何もしない |
| 上記以外 | 画面を読んで分類する (`agent_status` は見ない) |
| ネイティブの auto-continue が再開を予約している | 手を引く (待機中なら監視へ戻す) |
| 上限が解除済みで Enter 待ち | 通常の待機に入り、送信では Enter だけを送る |
| 待っても解除されない上限 (spend limit / usage credit) | 待機に入らない |
| 画面が limit と判定され、ゲートを通過した | 待機に入る |
| 待機中に limit が消えた | 監視に戻る (ユーザーが自分で再開した。プロンプトは送らない) |
| 待機期限に達したが、Claude Code が `blocked` で kind が `permission` / `question` / `auth` | 送らず待機を延長する (利用者が答える prompt を esc / Enter で代行しない。[program-status.md](program-status.md)) |
| 待機期限に達した | 再開プロンプトを送り、試行回数を増やす |
| 試行回数が `maxRetries` に達した | 長いクールダウンに入る |
| 一時的なサーバーエラー (5xx / 過負荷) | 指数バックオフで短周期リトライ |
| どれにも該当しない | 何もしない |

この表が暗に置いている不変条件 (期限前や prompt の開いた pane に送らない、Enter 待ちにテキストを送らない、監視中は
試行回数が 0、起床時刻は今 + margin より前にならない) は `internal/monitor/invariants_test.go` が乱択の tick 列で確かめる。

## 出力されるファイル

設定は利用者が書くものなので XDG の config ディレクトリに置く。ランタイム状態は揮発物なので
ハーネスの `~/.claude/` に混ぜず、XDG Base Directory の state ディレクトリ
`${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` (以下 `$STATE`) に置く。
`XDG_STATE_HOME` は daemon と `statusline` / `ingest-statusline` / `ingest-task` / `park` / `edge` / `board-log` (`apppath.FromEnv`) が同じ規則で解決する。

| パス | 書く主体 | いつ | 中身 | 消す主体 |
| ---- | ---- | ---- | ---- | ---- |
| `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` | 人間 | 手で編集したとき | 設定 | 消さない |
| `$STATE/rate-limits/<session_id>.json` | `agents-daemon statusline` または `ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと (`rate_limits.five_hour.resets_at` があるときだけ) | そのセッションの 5 時間 / 7 日ウィンドウの使用率と reset 時刻、観測時刻、session_id | daemon が 24 時間で消す |
| `$STATE/context/<session_id>.json` | `agents-daemon statusline` または `ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと | そのセッションの context 使用率、観測時刻、session_id、セッション名、多重化ツールの場所 | daemon が 7 日で消す |
| `$STATE/cost/<YYYY-MM-DD>/<session_id>.json` | `agents-daemon statusline` (`CLAUDE_STATUSLINE_SPEND_CAP` があるとき) | statusline の描画ごと (累計が変わったときだけ書き直す) | そのセッションの累計 cost と前日までの繰り越し (`internal/statusline/spend`)。daemon は読まない | `statusline` がその日の最初の書き込みで 45 日より古い日を消す |
| `$STATE/tasks/<domain>/<session_id>/<task_id>.json` と `…/todos.json` | `agents-daemon ingest-task` (PostToolUse hook `hooks/ingest-task.sh` が呼ぶ) | TaskCreate / TaskUpdate / TaskList / TodoWrite の呼び出しごと | タスク 1 つ (subject、status、blockedBy、session_id、cwd) と TodoWrite のリスト全体 (`internal/tasklog`)。`<domain>` は `home` か `work` (config の `homePaths` / `defaultDomain`)。読み手はタスクのボード (`status --board`、`internal/board`) | daemon が完了したタスクを 1 日、それ以外を 7 日 (更新が無ければ) で消す。`park adopt` (ボードの「パーキングへ移す」) がメモへ移したタスクを消す |
| `$STATE/parking/<domain>/<id>.json` | `agents-daemon park add` / `park set` / `park discard` / `park adopt` | メモを置いたとき、status を書き換えたとき (まとめて捨てたときを含む)、終了したセッションのタスクをメモへ移したとき (kind `task`) | パーキングのメモ 1 つ (kind、title、detail、files、default_taken、repo、sources (session_id、置いたときの進行中の task_id、cwd、まとめたときに後から置いた側の detail、時刻)、status) (`internal/parking`)。repo はボードの行と同じ main worktree のパス。domain、repo、正規化した title が同じ open のメモには sources を足す。`.lock` は書き込みを直列にするロック | daemon が open を 90 日、handed / discarded を 7 日 (更新が無ければ) で消す。同じメモが置き直されると数え直す。`.lock` は消さない |
| `$STATE/edges/<domain>/<id>.json` | `agents-daemon edge add` / `edge rm` (作業を渡す側が `agents-daemon:peer-sessions` skill の手順で呼ぶ。ボードの `Add blocker` / `Unlink` も) | 辺を書いたとき、消したとき | セッションをまたぐ辺 1 本 (before と after の端点 (参照、cwd、repo)、note、created_at) (`internal/edge`)。辺は「before が済まないと after を始められない」。id は向きを含めた両端の参照から決まり、同じ辺の登録し直しは同じファイルを書き直す。両端の domain が違う辺は書かない。読み手は `edge list` とボードの DAG (`status --board` の `graph`) | daemon が 7 日 (更新が無ければ) で消す。登録し直すと数え直す。`edge rm` が消す |
| `$STATE/daemon.lock` | daemon (`--ensure` も試しに開く) | 起動時に `flock(LOCK_EX|LOCK_NB)` で取り、寿命の間持つ | 空。起動の排他の対象で、lock を持つ process が daemon | 消さない (process の終了で OS が lock を解放する) |
| `$STATE/daemon.pid` | daemon | lock を取った後に作成、終了時に自分の pid が書いてあれば削除 | 稼働中デーモンの PID (`status` / `doctor` / `stop` が読む) | daemon。`stop` が stale なものを消す |
| `$STATE/status.json` | daemon | 毎 tick (既定 5 秒) | backend (rex)、監視中の pane 一覧と各 pane の自己申告 (`agent_status` と、あれば `agent_kind` / `agent_message`)・監視状態・試行回数・待機期限・直近の判定 | 上書き |
| `$STATE/logs/daemon.log` | daemon | 報告に値する出来事があったときだけ | 上限検知と待ち時間、再開送信、ユーザーの自己再開、ゲート抑制、ネイティブへの譲り、エラー | rotate |
| `$STATE/logs/daemon.log.YYYY-MM-DD` | daemon | 日付が変わって最初に書くとき | 前日までの `daemon.log` をそのまま退避したもの | daemon が 7 日で消す |
| `$STATE/limit-events/<YYYY-MM-DD>.jsonl` | daemon | 上限の出来事 (検知して待機に入った、送った、抑制した、ネイティブに譲った、Enter 待ち、hard cap、prompt で見送り) で daemon.log に行を書くのと同じ遷移のとき | 1 出来事 1 行の JSON (`internal/daemon/limitlog`): 判定した画面そのもの (末尾 `readLines` 行)、分類 (kind)、outcome、Claude Code の自己申告 (record)、block の高さと screen (`size` / `modes`)、own と account の利用上限 state、`readLines` / `detectionTailLines`。読み方は verification.md「次に上限を踏んだとき」 | daemon が 30 日で消す |
| `$STATE/logs/board-<domain>.log` | `agents-daemon ingest-task` / `park add` / `park set` / `park discard` / `park adopt` と、mod (`hooks/log.ts`) が呼ぶ `agents-daemon board-log` | タスクの記録、メモの操作、mod の出来事 (tool の登録の失敗、ボードの数と読み込みの失敗、送信の結果) があったとき。mod は同じ失敗と同じ数を書き直さない | ボードの経路の出来事を 1 行 1 件 (`internal/boardlog`、出来事の一覧は package doc)。session ID、task ID、メモの id、件数、失敗の理由だけで、subject、title、detail、送った文面は書かない。`<domain>` で分け、業務の出来事を私用の側に出さない。`logs --board` が読む | rotate |
| `$STATE/logs/board-<domain>.log.YYYY-MM-DD` | 上と同じ | 日付が変わって最初に書くとき | 前日までの `board-<domain>.log` をそのまま退避したもの | daemon が 7 日で消す |
| `$STATE/logs/build.log` | `hooks/build-daemon.sh` | SessionStart が build を起こしたとき | build の開始・完了・失敗 | 消さない (追記) |
| `$STATE/logs/dream-harvest.log` | Stop hook が detach した `agents-daemon dream harvest` | 失敗したときだけ (成功は無音) | 時刻付きの失敗の行と stderr | 消さない (追記) |
| `$STATE/dream/harvest/<YYYY-MM-DD>.jsonl` | `agents-daemon dream harvest` (Stop hook と手動) | Stop のたび | 1 セッション 1 行の transcript の要約 (PII 除去済み)。日付は ts_start | `dream harvest` が 90 日で消す |
| `$STATE/dream/harvest-state.json` | `agents-daemon dream harvest` | Stop のたび | `--incremental` の前回実行時刻 | 上書き |
| `$STATE/dream/candidates/<slug>-<YYYY-MM-DD>.jsonl` | `agents-daemon dream mine` | 手動 | memory → skill の統合候補と重複 memory の削除候補 | `dream harvest` が 90 日で消す |
| `$STATE/dream/replays/<candidate-id>.json`、`$STATE/dream/verdicts/<candidate-id>.json` | `agents-daemon:dream` skill の Replay / Gate (LLM が Write する) | 手動 | 旧新の挙動予測と 3 judge の verdict | `dream harvest` が 90 日で消す |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` | `hooks/build-daemon.sh` / `task install` | build 時 | 実行ファイル | 消さない |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon.version` | `hooks/build-daemon.sh` | build の成功時 | 建てた元の plugin version | 消さない |

`$STATE` はデーモンが必要に応じて作る。消しても壊れない
(次回の起動と次回の statusline 描画で再生成される)。

旧版 (compact の自動化と ttl-guard を持っていた版) が書いていた `compact-state/` `compacted/` `cache/`
`cache-ack/` `cache-idle-compacted/` は今の版では読みも消しもしない。残っていれば手で消す。

静かな tick は**何も書かない**。ログが伸びていないことは、何も起きていないことを意味する。

### ログの rotate と自動削除

現行ログの名前は `daemon.log` で固定する。`tail -f` や `logs` サブコマンドの参照先が
日付で変わらないようにするため。日付が変わって最初に書くとき、それまでの `daemon.log` を
最終書き込み日の名前 (`daemon.log.YYYY-MM-DD`) へ rename してから新しいファイルへ書く。
daemon の再起動を跨いで日付が変わっていた場合も、mtime から最終書き込み日を取って同じように
退避する。退避先が既にあるとき (時計の巻き戻り等) は上書きせず、次の日付境界まで
`daemon.log` へ追記し続ける。

ボードの経路のログ (`board-<domain>.log`) も同じ規則で退避する。書くのは短命な CLI なので、1 行ごとに開いて閉じる。
並行するプロセスの行は O_APPEND と 1 回の write で混ざらない。

古いファイルは daemon 自身が消す。起動時と 1 時間ごとに、mtime が保持期間を過ぎた
rotate 済みログ (`daemon.log` と `board-<domain>.log`、7 日)、`rate-limits/` 配下の state (24 時間。ingest-statusline が rename 前に
死んで残った `.rate-limits.*` も同じ扱い)、`context/` 配下 (7 日) を削除する。何かを消したときだけログに残す。

保持期間は設定キーにせず定数 (`internal/daemon/housekeeping.go`、`internal/ratelimit` と `internal/sessioncontext` の `Retention`) に置く。
state の 24 時間は判定に影響しない値として選んである: `resets_at` は観測から高々 5 時間先で、
起床時刻の算出 (`LatestWindow`) は未来の `resets_at` しか使わない。`context/` を 7 日と長く取るのは、
block から session への対応を 5 時間の上限待ちを跨いで残すため。
