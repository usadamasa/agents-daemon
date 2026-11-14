# agents-daemon の全体構成と出力ファイル

## 全体の連動

statusline・hook・daemon が別々のタイミングで動き、ファイルを介して繋がる。
互いを直接呼ばない。

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
                                              │  (ここで上限かどうかを判定。下の表)
                                              │
[3]                                           └─ 上限解除の時刻まで待つ
                                                 ├─ PaneRead ──────────> 待機明けに画面の通知欄を読んでキーを選ぶ
                                                 └─ SendText / SendKeys で再開 (backend は rex。multiplexer.md)
```

context の compact と prompt cache の失効には関わらない。Claude Code 本体の autocompact・idle compaction と、
失効後の footer の案内 (`~222k uncached · /clear to start fresh`) に任せる。

### [1] statusline が block と session の対応を渡す

daemon が pane の transcript を引くには、その pane のセッションの ID が要る。`statusLine.command` に向けたバイナリの
`statusline` か、利用者の statusline から stdin を受けた `ingest-statusline` が、`context/<session_id>.json`
(毎回。`REX_BLOCK` から block との対応を書く) と `rate-limits/<session_id>.json`
(stdin に `rate_limits` があるときだけ) に落とす。上限の判定と解除時刻は transcript の末尾の entry から取るので、
`rate-limits/` を daemon は読まない (`doctor` が statusline の配線の確認に出すだけ)。statusline の記録が無い pane は
rex の前景の pid で Claude Code の登録簿 (`~/.claude/sessions/`) から補う。stdin の中身、state file の出し分け、書式は
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
回収したときだけ `systemMessage` で知らせ、state は書かない。判定の条件の実体は `worktree` の package doc、
手で叩く形は [operations.md](../../../../skills/agents-daemon/references/operations.md) の
「放置された worktree を回収する」。
<!-- cc-premise: 2.1.310 worktrees 本体の sweep は subagent / background session の worktree だけを消し、foreground の keep は残す -->
subagent の worktree (`claude agent` の lock) は Claude Code 本体の periodic sweep (`cleanupPeriodDays`) が消すので
触らない。daemon の housekeeping にしなかったのは、daemon が rex の外では起きず、対象リポジトリの一覧も cwd も
持たないため (#97)。

plugin は mod も持ち、依存図は別 module のアプリ (`gui/`) が描く。依存図の JSON、アプリ、セッションへの送達 (`tell`)、
渡した宛先の辺の通知は [deps.md](deps.md)、パーキングのメモと手渡し (`park hand`)、park tool、ファイルのメモの通知は
[parking.md](parking.md)。

バイナリ自体も SessionStart hook が用意し、差し替わったら動いている daemon が自分で入れ替わる。build の条件、
lock、入れ替わりは [binary.md](binary.md)。

### [3] デーモンが監視して再開する

単一プロセスが全 Claude pane を見る。pane ごとの状態は pane ID (rex の block ID) をキーに
プロセス内で持つ。pane の列挙・読み取り・送信は多重化ツールの backend (rex) 越しに行う。
backend の判定と rex の呼び方は [multiplexer.md](multiplexer.md)。上限の判定は transcript、画面は待機明けのキー選択だけ
([rate-limit.md](rate-limit.md))。

## CLI の組み立て

`cmd/agents-daemon` は `main` と root の登録だけを持つ。コマンドの組み立て (flag、説明文、環境を `internal/` の
本体へ渡す配線) は機能ごとの `<family>cli` パッケージが持ち、公開するのは root に載せる
`Commands() []*cobra.Command` の 1 つだけにする。判定や組み立ての規則は `internal/` の本体に置き、cli パッケージは
引数を読んで呼ぶだけにする。

| family | コマンド |
| ---- | ---- |
| `depscli` | deps (依存図の JSON)、edge、ingest-task、task dismiss (依存図に載る記録を書く・読む・消す)、event-log (出来事のログに mod の出来事を書く) |
| `parkcli` | park (hand を含む)、spawn、tell (パーキングロットのメモ、メモを渡す先のセッションを起こす、セッションの入力欄に打つ) |
| `daemoncli` | daemon、stop、status、logs、doctor、inspect、simulate (daemon のライフサイクルと読むだけの診断)、ingest-turn-failure (daemon が上限の出来事に添える記録を書く) |
| `dreamcli` | dream |
| `statuslinecli` | statusline、ingest-statusline |
| `worktreecli` | prune-worktrees |
| `jsonflag` | family ではなく root のグローバルオプション `--json` の配線。対応はコマンドが `Annotations` で宣言し、宣言の無いコマンドに付いたら RunE の前に error にする |
| `statecmd` | family ではなく、state を読み書きして結果を JSON で出すコマンド (park、spawn、edge) の共通の走らせ方 |

cobra を import するのは `cmd/` の下だけで、`internal/` の本体は CLI を知らない。Go の internal 規則で
`internal/` からは cli パッケージを import できず、`.go-arch-lint.yml` は cobra を `cmd` (`cmd/**`) にだけ許す。
コマンドを足すときは、呼ぶ本体に合う family に置き、合うものが無ければ family を足して root に 1 行登録する。

## 毎 tick の判定

pane ごとに次を上から順に評価する。上限側の詳細 (分類、待つかどうか、起床時刻) は [rate-limit.md](rate-limit.md)、
画面の読み取りと送信の作法は [pane-io.md](pane-io.md)。

| 条件 | 動作 | outcome |
| ---- | ---- | ---- |
| 待機中で、まだ待機期限に達していない | 何もしない | `still-waiting` |
| pane の session_id が引けない、transcript の末尾を読めない | 判定しない (待機中なら延長する) | `no-session` / `transcript-unavailable` |
| record が `working` | 触らない (待機中なら誰かが再開したので監視へ戻る。transcript が上限なら食い違いを記録) | `monitoring` / `recovered-by-user` / `suppressed-working` |
| 監視中: 待っても解除されない上限 (spend limit / usage credit / Fable のモデル単位の上限) | 待機に入らない | `hard-cap` |
| 監視中: 週の上限で解除が 24 時間より先 | 待機に入らない (毎 tick 見直す) | `weekly-cap` |
| 監視中: 解除 (解除時刻の無い一時エラーは entry) から 24 時間より経った、または時刻が読めない | 待機に入らない | `stale-turn` |
| 監視中: 上限 (5 時間 / 24 時間以内の週) | 解除 + margin まで待つ | `waiting` |
| 監視中: 一時的なサーバーエラー (5xx / 過負荷 / 429 の Request rejected) | 指数バックオフで待つ (`handleTransient`) | `waiting` |
| 待機明け: transcript が上限でなくなった | 監視に戻る (利用者が自分で再開した。プロンプトは送らない) | `recovered-by-user` |
| 待機明け: 別の entry の上限で解除時刻が未来 | 送らず待ち直す (試行回数は持ち越す) | `waiting` |
| 待機明け: Claude Code が `blocked` で kind が `permission` / `question` / `auth` | 送らず待機を延長する (利用者が答える prompt を esc / Enter で代行しない。[program-status.md](program-status.md)) | `blocked-by-prompt` |
| 待機明け: 画面を読めない | 送らず次の tick で読み直す | `read-error` |
| 待機明け: 通知欄がネイティブの auto-continue の予約、request の中のリトライ、lower-priority mode | 手を引く (監視へ戻る) | `native-auto-continue` / `lower-priority` |
| 待機明け: 試行回数が `maxRetries` に達した | 長いクールダウンに入る | `max-retries` |
| 待機明け: 通知欄が解除済みで Enter 待ち | Enter だけを送る | `resume-enter` |
| 待機明け | esc → `retryMessage` → enter を送り、試行回数を増やす | `retried` |

この表が暗に置いている不変条件 (画面を読むのは待機明けで transcript が上限のときだけ、期限前や working や
prompt の開いた pane に送らない、Enter 待ちにテキストを送らない、監視中は試行回数が 0、起床時刻は今 + margin より前に
ならない、解除から 24 時間より経った entry や解除が 24 時間より先の週の上限で待機に入らない) は
`monitor/invariants_test.go` が乱択の tick 列で確かめる。

## 出力されるファイル

設定は利用者が書くものなので XDG の config ディレクトリに置く。ランタイム状態は揮発物なので
ハーネスの `~/.claude/` に混ぜず、XDG Base Directory の state ディレクトリ
`${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` (以下 `$STATE`) に置く。
`XDG_STATE_HOME` は daemon と `statusline` / `ingest-statusline` / `ingest-task` / `ingest-work` / `work` / `ingest-turn-failure` / `park` /
`edge` / `event-log` / `deps` (`apppath.FromEnv`) が同じ規則で解決する。

消す主体の列の保持期間は、記録を書くパッケージの定数が根拠のコメントと一緒に持つ。依存図とパーキングロットが読む記録
(tasks、edges、sessions、context、parking) の保持期間の一覧と、その間の関係 (辺は端点のタスクのノードより長く残る、台帳は
辺より長く残る、古いメモの区切りは open のメモが消えるより前、など) は `daemon/retention_test.go` が表で固定する。
値か関係を変えるときは、そのテストとこの表を一緒に直す。

| パス | 書く主体 | いつ | 中身 | 消す主体 |
| ---- | ---- | ---- | ---- | ---- |
| `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` | 人間 | 手で編集したとき | 設定 | 消さない |
| `$STATE/rate-limits/<session_id>.json` | `agents-daemon statusline` または `ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと (`rate_limits.five_hour.resets_at` があるときだけ) | そのセッションの 5 時間 / 7 日ウィンドウの使用率と reset 時刻、観測時刻、session_id。daemon は読まず、`doctor` が statusline の配線の確認に出す | daemon が 24 時間で消す |
| `$STATE/context/<session_id>.json` | `agents-daemon statusline` または `ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと | そのセッションの context 使用率、観測時刻、session_id、セッション名、多重化ツールの場所 | daemon が 7 日で消す |
| `$STATE/cost/<YYYY-MM-DD>/<session_id>.json` | `agents-daemon statusline` (`CLAUDE_STATUSLINE_SPEND_CAP` があるとき) | statusline の描画ごと (累計が変わったときだけ書き直す) | そのセッションの累計 cost と前日までの繰り越し (`statusline/spend`)。daemon は読まない | `statusline` がその日の最初の書き込みで 45 日より古い日を消す |
| `$STATE/tasks/<session_id>/<task_id>.json` と `…/todos.json` | `agents-daemon ingest-task` (PostToolUse hook `hooks/ingest-task.sh` が呼ぶ) | TaskCreate / TaskUpdate / TaskList / TodoWrite の呼び出しごと | タスク 1 つ (subject、status、blockedBy、session_id、cwd) と TodoWrite のリスト全体 (`tasklog`)。読み手は依存図 (`deps`、`deps` パッケージ) | daemon が完了したタスクを 1 日、それ以外を 7 日 (更新が無ければ) で消す。pane が見えている間は daemon が tick ごとに未完了のタスクと `todos.json` の mtime を進める。終了したセッションの完了タスクは作業の記録が消えるまで残す。`park adopt` (pane の「パーキングへ移す」) がメモへ移したタスクを、`task dismiss` (pane の `Dismiss`) が終了したセッションのタスクを消す |
| `$STATE/work/<session_id>.json` と `…/.lock` | `agents-daemon ingest-work` (SessionStart / UserPromptSubmit / Stop / SessionEnd hook `hooks/ingest-work.sh` が呼ぶ)、`work set` (アプリの「題を直す」、companion の mod `hooks/work-infer.ts`) | UserPromptSubmit と Stop で作り、SessionStart (resume) と SessionEnd は記録があるときだけ書き換える。`work set` は題を書き換えたとき | セッション 1 つの作業 (最初の prompt の 1 行目の subject と出どころ origin (prompt / human)、companion の推定の題 inferred、最後の応答の 1 行目 summary、turns、started_at、ended_at と end_reason、cwd) (`worklog`、#368)。読み手は依存図 (タスクの無いセッションのノード)。書き込みは `.lock` (flock) の中で読んでから自分の欄だけを書く (models/work)。`.lock` は書き込みを直列にするロック | daemon が 7 日 (更新が無ければ) で消す。pane が見えている間は daemon が tick ごとに mtime を進めるので、消えるのは終了してからの経過。`park adopt` (`sid:`) がメモへ移した作業を、`task dismiss` (`sid:`) が終了したセッションの作業を消す。`.lock` は消さない |
| `$STATE/parking/<id>.json` | `agents-daemon park add` / `park set` / `park discard` / `park withdraw` / `park adopt` | メモを置いたとき、status を書き換えたとき (まとめて捨てたときを含む)、終了したセッションのタスクをメモへ移したとき (origin `adopted`) | パーキングのメモ 1 つ (kind (aside / scope / preference)、origin (placed / adopted)、title、detail、files、default_taken、repo、sources (session_id、置いたときの進行中の task_id、cwd、まとめたときに後から置いた側の detail、時刻)、status、handed のときの宛先 handed_to (`sid:<session_id>` / `repo:<path>`)) (`parking`)。repo は依存図の repo と同じ main worktree のパス。repo と正規化した title が同じ open のメモには sources を足す。`.lock` は書き込みを直列にするロック | daemon が open を 90 日、handed / discarded を 7 日 (更新が無ければ) で消す。同じメモが置き直されると数え直す。`.lock` は消さない |
| `$STATE/edges/<id>.json` と `…/.lock` | `agents-daemon edge add` / `edge rm` (作業を渡す側が `agents-daemon:peer-sessions` skill の手順で呼ぶ。pane の `Add blocker` / `Unlink` も)。daemon が `session:<名前>` の端点を結びつけて書き直す。daemon (`refpoll`) が PR を出したセッションから PR への辺を導く | 辺を書いたとき、消したとき、daemon が端点を結びつけたとき・辺を導いたとき (tick ごと) | セッションをまたぐ辺 1 本 (before と after の端点 (参照、cwd、repo、結びつけた元の参照 `bound_from`)、note、daemon が導いた印 `derived`、created_at) (`edge` パッケージ)。辺は「before が済まないと after を始められない」。id は書いたときの両端の参照から決まり、結びつけても変わらない。同じ辺の登録し直しは同じファイルを書き直す。`.lock` は書き込みを直列にするロック。読み手は `edge list` と依存図の DAG (`deps` の `graph`) | daemon が 7 日 (更新が無ければ) で消す。登録し直すと数え直す。daemon が tick ごとに、生きているセッションを端点に持つ辺と、写しが open の参照を before に持つ辺の mtime を進める。`edge rm` が消す。daemon が tick ごとに、before の issue / PR の写し (`refs/`) が merged / closed の辺を消す (`refpoll`、dropped の辺は残す)。`task dismiss` (`sid:`) が終了したセッションを端点に持つ辺を消す。`.lock` は消さない |
| `$STATE/sessions/<session_id>.json` | daemon (`ledger`) だけ | tick ごと (status.json を書いた直後) | セッション台帳のセッション 1 つ (session_id、名前、cwd、repo、first_seen、last_seen)。first_seen は最初に見た時刻のまま動かず、last_seen は pane が見えている間進む。daemon が辺の `session:<名前>` の端点を結びつけるのに使う | daemon が 7 日 (見えなければ) で消す |
| `$STATE/spawned-sessions/<id>.json` | `agents-daemon spawned-session mark` (spawn と peer-sessions skill が起こした rex session の中で、claude の包みの sh が呼ぶ) | claude を起こす前 | 起こした rex session の印 1 つ (session_id、created_at) (`spawnedsession`)。id は session ID から決める。daemon (`daemon/reaper`) が 30 秒ごとに回り、block が 0 の session を閉じる (#557) | daemon が session を閉じたとき、session が無くなっていたときに消す。古さでは消さない (daemon が回らないまま日が経っても、生きている session の印を残す) |
| `$STATE/refs/<id>.json` | daemon (`refstate`) だけ | tick ごとに、辺の端点の issue / PR のうち引き直す時期のものを 1 つ gh で引いたとき (まだ引いていない参照と、open で前回から 5 分経った参照。閉じた参照は引き直さない) | 参照 1 つの状態 (ref、state (open / merged / closed / dropped)、title、open の PR の段 pr (draft、ci、review、merge)、PR の head branch head_branch、checked_at)。closed は completed で閉じた issue、dropped はマージせずに閉じた PR と not planned / duplicate で閉じた issue。id は参照から決める。gh が失敗したら書かない。読み手は依存図 (`deps` の `graph`) で、merged / closed の参照を完了に、dropped の参照を取りやめにし、title を参照のノードの題にする (辺の note には混ぜない) | daemon が 7 日 (書き直さなければ) で消す。辺が残っていれば次の引き直しで書き直す |
| `$STATE/daemon.lock` | daemon (`--ensure` も試しに開く) | 起動時に `flock(LOCK_EX|LOCK_NB)` で取り、寿命の間持つ | 空。起動の排他の対象で、lock を持つ process が daemon | 消さない (process の終了で OS が lock を解放する) |
| `$STATE/daemon.pid` | daemon | lock を取った後に作成、終了時に自分の pid が書いてあれば削除 | 稼働中デーモンの PID (`status` / `doctor` / `stop` が読む) | daemon。`stop` が stale なものを消す |
| `$STATE/status.json` | daemon | 毎 tick (既定 5 秒) | backend (rex)、監視中の pane 一覧と各 pane の自己申告 (`agent_status` と、あれば `agent_kind` / `agent_message`)・監視状態・試行回数・待機期限・直近の判定 | 上書き |
| `$STATE/logs/daemon.log` | daemon | 報告に値する出来事があったときだけ (同じ状態が続く間は書かない) | 上限検知と待ち時間、再開送信、ユーザーの自己再開、working の抑制、待たない理由 (hard cap、週の上限、stale-turn)、判定できない理由、ネイティブへの譲り、エラー | rotate |
| `$STATE/logs/daemon.log.YYYY-MM-DD` | daemon | 日付が変わって最初に書くとき | 前日までの `daemon.log` をそのまま退避したもの | daemon が 7 日で消す |
| `$STATE/limit-events/<YYYY-MM-DD>.jsonl` | daemon | 上限の出来事 (検知して待機に入った、送った、手を引いた、Enter 待ち、hard cap、週の上限、prompt で見送り) と、判定できなかった理由 (session_id が引けない、transcript が読めない、transcript は上限なのに record が working) で daemon.log に行を書くのと同じ遷移のとき | 1 出来事 1 行の JSON (`daemon/limitlog`): 判定の根拠にした transcript の末尾の entry (`transcript`: パス、uuid、error、`quota_limits`、本文の頭) か読めなかった理由 (`reason`)、分類 (kind)、outcome、Claude Code の自己申告 (record)、待機明けに読んだ画面と通知欄の判定 (`screen` / `hint`、送った tick だけ)、block の高さと screen (`size` / `modes`)、`readLines` / `detectionTailLines`、そのセッションの turn の失敗 (`turn_failure`、あるときだけ)。読み方は verification.md「次に上限を踏んだとき」 | daemon が 30 日で消す |
| `$STATE/turn-failures/<session_id>.json` | `agents-daemon ingest-turn-failure` (StopFailure / Stop / UserPromptSubmit hook `hooks/ingest-turn-failure.sh` が呼ぶ) | StopFailure で書き、Stop と UserPromptSubmit で消す | そのセッションの最後の turn を終わらせた API error (`error`、`error_details`、`last_assistant_message`、観測時刻。`turnfailure`)。あるのは「最後の turn が API error で終わり、次の turn がまだ始まっていない」間だけ。読み手は daemon の上限の出来事の記録 | 次の turn の hook が消す。閉じたセッションの残りは daemon が 24 時間で消す |
| `$STATE/logs/events.log` | `agents-daemon ingest-task` / `ingest-work` / `work set` / `park add` / `park set` / `park discard` / `park withdraw` / `park adopt` / `park hand` / `task dismiss` / `tell` と、mod (`hooks/log.ts`) が呼ぶ `agents-daemon event-log` | タスクの記録、作業の記録 (作ったとき、終了したとき、題を書き換えたとき。ターンごとの更新は書かない)、メモの操作、アプリからの送達 (tell、park hand)、mod の出来事 (tool の登録の失敗、通知の失敗、題の推定) があったとき。mod は同じ失敗を書き直さない | 依存図とパーキングロットの経路の出来事を 1 行 1 件 (`eventlog`、出来事の一覧は package doc)。session ID、task ID、メモの id、件数、失敗の理由だけで、subject、title、detail、送った文面は書かない。`logs --events` が読む | rotate |
| `$STATE/logs/events.log.YYYY-MM-DD` | 上と同じ | 日付が変わって最初に書くとき | 前日までの `events.log` をそのまま退避したもの | daemon が 7 日で消す |
| `$STATE/logs/build.log` | `hooks/build-daemon.sh`、`hooks/build-gui.sh` | SessionStart が build を起こしたとき | build の開始・完了・失敗・飛ばした理由 | 消さない (追記) |
| `$STATE/build.failed`、`$STATE/build-gui.failed` | `hooks/build-daemon.sh`、`hooks/build-gui.sh` (`hooks/lib/go-build.sh`) | go build が失敗したとき | 1 行目が go の最後の行 (原因)、2 行目が `!` で打つ建て直しのコマンド。次の SessionStart で `hooks/ensure-daemon.sh` が、その成果物を建て直すときだけ 1 行目を systemMessage に載せる (建て直すコマンドは今の plugin の置き場から組む) | 同じ script が建てられたときに消す |
| `$STATE/logs/dream-harvest.log` | Stop hook が detach した `agents-daemon dream harvest` | 失敗したときだけ (成功は無音) | 時刻付きの失敗の行と stderr | 消さない (追記) |
| `$STATE/dream/harvest/<YYYY-MM-DD>.jsonl` | `agents-daemon dream harvest` (Stop hook と手動) | Stop のたび | 1 セッション 1 行の transcript の要約 (PII 除去済み)。日付は ts_start | `dream harvest` が 90 日で消す |
| `$STATE/dream/harvest-state.json` | `agents-daemon dream harvest` | Stop のたび | `--incremental` の前回実行時刻 | 上書き |
| `$STATE/dream/candidates/<slug>-<YYYY-MM-DD>.jsonl` | `agents-daemon dream mine` | 手動 | memory → skill の統合候補と重複 memory の削除候補 | `dream harvest` が 90 日で消す |
| `$STATE/dream/replays/<candidate-id>.json`、`$STATE/dream/verdicts/<candidate-id>.json` | `agents-daemon:dream` skill の Replay / Gate (LLM が Write する) | 手動 | 旧新の挙動予測と 3 judge の verdict | `dream harvest` が 90 日で消す |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` | `hooks/build-daemon.sh` / `task install` | build 時 | 実行ファイル | 消さない |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon.version` | `hooks/build-daemon.sh` | build の成功時 | 建てた元の plugin version | 消さない |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agent-tasks` | `hooks/build-gui.sh` (`build-daemon.sh` の末尾と、daemon だけ最新のときの SessionStart、`task gui:install` が呼ぶ) | build 時 (macOS で go と Xcode Command Line Tools があるときだけ) | 依存図のアプリの実行ファイル | 消さない |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agent-tasks.version` | `hooks/build-gui.sh` | build の成功時 (飛ばしたときは書かない) | 建てた元の plugin version | 消さない |
| `~/Applications/agent-tasks.app` | `hooks/build-gui.sh` | build の成功時 (一時の bundle を組んで rename で入れ替える) | 起動用の .app (上の実行ファイルの写し、`gui/darwin/Info.plist`、`gui/darwin/agent-tasks.icns`、ad hoc 署名)。起動中のアプリは元の inode を持ち続け、stamp が新しくなったのを見て切り替えのボタンを出し、押されたら新しい .app へ入れ替わる (`gui/upgrade.go`) | 消さない |
| `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/gui/restore.json` | 起動中の agent-tasks (`gui/upgrade.go`) | 切り替えのボタンが押され、新しいバージョンへ入れ替わる直前 | 窓の位置と大きさ、画面の状態 (frontend の `restore.js` が鍵ごとに詰める) | 次に起きた agent-tasks が起動時に読んで消す (2 分より古いか version が合わなければ読まずに消す)。起こせなかったときは書いた側が消す |

`$STATE` はデーモンが必要に応じて作る。消しても壊れない
(次回の起動と次回の statusline 描画で再生成される)。

静かな tick は**何も書かない**。ログが伸びていないことは、何も起きていないことを意味する。

### ログの rotate と自動削除

現行ログの名前は `daemon.log` で固定する。`tail -f` や `logs` サブコマンドの参照先が
日付で変わらないようにするため。日付が変わって最初に書くとき、それまでの `daemon.log` を
最終書き込み日の名前 (`daemon.log.YYYY-MM-DD`) へ rename してから新しいファイルへ書く。
daemon の再起動を跨いで日付が変わっていた場合も、mtime から最終書き込み日を取って同じように
退避する。退避先が既にあるとき (時計の巻き戻り等) は上書きせず、次の日付境界まで
`daemon.log` へ追記し続ける。

出来事のログ (`events.log`) も同じ規則で退避する。書くのは短命な CLI なので、1 行ごとに開いて閉じる。
並行するプロセスの行は O_APPEND と 1 回の write で混ざらない。

古いファイルは daemon 自身が消す。起動時と 1 時間ごとに、mtime が保持期間を過ぎた
rotate 済みログ (`daemon.log` と `events.log`、7 日)、`rate-limits/` 配下の state (24 時間。ingest-statusline が rename 前に
死んで残った `.rate-limits.*` も同じ扱い)、`context/` 配下 (7 日) を削除する。何かを消したときだけログに残す。

保持期間は設定キーにせず定数 (`daemon/housekeeping.go`、`ratelimit` と `sessioncontext` の `Retention`) に置く。
`rate-limits/` の 24 時間は daemon の判定に関わらない (読むのは `doctor` だけ)。`context/` を 7 日と長く取るのは、
block から session への対応を上限待ち (週の上限は最長 24 時間) を跨いで残すため。
