# agents-daemon の全体構成と出力ファイル

## 全体の連動

statusline・hook・daemon が別々のタイミングで動き、ファイルを介して繋がる。
互いを直接呼ばない。

```
[1] Claude Code ──毎描画──stdin──> agents-daemon statusline (statusLine.command)
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
                                              │  (backend は rex。下の「多重化ツールの backend」)
                                              │
                                              │  ここで判定 (下の表)
                                              │
[3]                                           └─ 上限解除の時刻まで待つ
                                                 └─ SendText / SendKeys で再開
```

context の compact と prompt cache の失効には関わらない。Claude Code 本体の autocompact・idle compaction と、
失効後の footer の案内 (`~222k uncached · /clear to start fresh`) に任せる。

### [1] statusline が上限情報を渡し、バイナリが落とす

<!-- cc-premise: 2.1.299 statusline stdin の rate_limits.five_hour / context_window と再実行の契機 -->

Claude Code は statusline コマンドの stdin に JSON を渡す。その中に
`rate_limits.five_hour.{used_percentage, resets_at}` が入る (`resets_at` は Unix epoch 秒)。
この値は statusline の stdin にしか来ないので、statusline を経由するのは避けられない。
配線は 2 通りで、取り出しはどちらもバイナリ (`internal/sessionstate.Store.IngestStatusline`) が持つ
(`agents-daemon:setup` skill の `references/statusline.md`)。

- `statusLine.command` をバイナリの `statusline` に向ける (推奨)。描画 (`internal/statusline`) と state file の
  書き込みを同じプロセスで行う。月額上限の env (`CLAUDE_STATUSLINE_SPEND_CAP`) があれば cost の台帳
  (`internal/spend`) にも記録する
- 利用者の statusline から stdin を `agents-daemon ingest-statusline` へ渡す 1 行を足す。state file だけを書く

state file の出し分けは次のとおり。

- `rate_limits` は**初回 API レスポンス後**にだけ現れる。
- 無いときは何も書かない。既存ファイルも消さない (まだ届いていないセッションが、他のセッションの
  書いた state を壊さないため)。
- **5 時間ウィンドウが切り替わる瞬間は `five_hour` が null で届く** (実機で `xx:10:01` の
  ような境界時刻に観測)。これも書かず、最後に持っていた有効な state を残す。null で
  上書きすると Go 側は 0 に読むため、待機明けのゲートが「fresh な 0%」で送信を抑制し、
  15 分後に state が古くなって改めて検知しても `resets_at` が無くフォールバック
  (5 時間待ち) に落ちる。2026-08-28 の取りこぼしはこれが原因だった。daemon 側も
  `resets_at` が 0 の state は「値が無い」として扱う (`ratelimit.State.HasFiveHour`)。
  信号にするのは `resets_at` だけ。観測されたのは両方 null の形で、`used_percentage`
  だけが null の描画は見ていない。
- **セッションごとに別ファイルへ書く**。`resets_at` はアカウント全体で共通だが、
  使用率は観測した時点の値で、セッションによって観測時刻が違う。1 ファイルを共有すると、
  最後に statusline を描いたセッションの数字で別セッションの pane が判定される。実際、
  2026-08-19 の取りこぼしでは上限に達したセッションの pane が、無関係なセッション
  (次のウィンドウで動き出した) の使用率 6% でゲート抑制された。ゲートは自セッションの
  ファイルだけを見る。他セッションのファイルは起床時刻の算出にだけ使う
  ([rate-limit.md](rate-limit.md) の「待機時刻の決め方」参照)。
- 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う (`internal/statefile`)。
- statusline の描画を壊さないよう fail-silent。失敗しても exit 0 で終わる (理由は stderr)。
  `ingest-statusline` は stdout に何も出さず、`statusline` は描画だけを出す。daemon のログにも書かない。

これがこのツールの中核。`resets_at` が epoch で直接手に入るので、画面の
`resets 12:30pm (Asia/Tokyo)` のような表記を am/pm やタイムゾーンごとパースする処理が要らない。
実機で `resets_at = 1786851000` と画面の `12:30pm (Asia/Tokyo)` が一致することを確認済み。

同じ呼び出しで `context/<session_id>.json` へも書く。こちらは `rate_limits` と違って**毎描画で必ず書く**。
daemon が使うのは `multiplexer` (block から session_id への対応) で、同じ block の記録が複数あれば
`observed_at` が最新のものを採る。`session_name` はボードのカードに出すセッションの名前になる。
`used_percentage` は参考として写すだけで、daemon は読まない。

#### statusline が書く state

`$STATE` は下の「出力されるファイル」と同じ `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`。
どちらも同一ディレクトリの一時ファイルへ書いてから rename で置き換える。
読み手 (daemon) と書き手 (`statusline` / `ingest-statusline`) は書式ごとのパッケージの同じ型を使うので、
書式はここに転記した参考で、実体は `rate-limits/` が `internal/ratelimit`、`context/` が `internal/sessioncontext` にある。

`$STATE/rate-limits/<session_id>.json` (`rate_limits.five_hour.resets_at` があるときだけ):

```json
{
  "five_hour": {"used_percentage": 42, "resets_at": 1786851000},
  "seven_day": {"used_percentage": 18, "resets_at": 1787300000},
  "observed_at": 1786840000,
  "session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b"
}
```

`seven_day` は入力にあるときだけ付ける。`used_percentage` は整数に丸める。

`$STATE/context/<session_id>.json` (毎描画):

```json
{"session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b", "session_name": "ad-board-cards", "used_percentage": 63, "observed_at": 1786840000, "multiplexer": {"kind": "rex", "session": "session:01a1…", "block": "block:01a1…"}}
```

`observed_at` はどちらも書いた時点の Unix epoch 秒。

`session_name` は statusline の stdin の `session_name` をそのまま写し、入力に無ければ付けない。
<!-- cc-premise: 2.1.302 statusline session_name は custom title、無ければ AI の題 -->
中身は `--name` か `/rename` で付けた名前、無ければ Claude Code が会話から付けた題になる。`ListAgents` の名前とは
同じとは限らない (同じ名前のセッションが既にいると `ListAgents` 側だけ `workspace-dc` のような接尾辞が付く)。
ボード (`internal/board`) はこの名前をカードとパーキングの `sources` に出し、名前が無ければ session ID の先頭 8 文字、
同じ名前のセッションが複数あれば名前の後ろに session ID の先頭を足す。記録は `context/` の保持期間 (7 日) の間残るので、
終了したセッションのカードとメモにも名前が付く。

`multiplexer` は Claude Code が動いている多重化ツールの場所。statusline のコマンドは Claude Code の環境を
継承するので、バイナリが環境変数から読んで書く (`sessioncontext.MultiplexerRefFromEnv`)。

| 環境変数 | 書く値 |
| ---- | ---- |
| `REX_BLOCK` がある | `{"kind": "rex", "session": $REX_SESSION, "block": $REX_BLOCK}` |
| 無い | フィールドを付けない |

backend は block から session_id をこの記録で逆引きする (`sessioncontext.Store.ListMultiplexerBlocks`)。同じ ID の
記録が複数あれば `observed_at` が最新のものを採る (同じ block で Claude Code を起動し直すと古い記録が残る)。
`context/` は 7 日で消えるだけなので、5 時間の上限待ちを跨いでも対応は残る。制約 (初回描画までの
数秒) は [operations.md](operations.md) の「既知の制約」。

skill がセッションの中から自分の session_id を引くときは `CLAUDE_CODE_SESSION_ID` を唯一の情報源にする
(`scripts/get-session-id.sh`)。

### [2] SessionStart hook がデーモンを起こす

`hooks/ensure-daemon.sh` が `agents-daemon daemon --ensure` を叩く。生きているデーモンが既にあれば
即座に戻り、無ければ自分自身を detach 起動して戻る。hook を絶対にブロックしない。

**配線は plugin の `hooks/hooks.json` にある。** 同じファイルの Stop に `hooks/dream-harvest.sh` があり、
同じバイナリの `dream harvest --incremental --detach` を exec する (バイナリが無ければ何もしない)。
こちらも detach して即座に戻り、子の stderr は `$STATE/logs/dream-harvest.log` に残る。
`dream` の役目と state は `agents-daemon:dream` skill の `references/cli.md`。

SessionStart には `hooks/prune-worktrees.sh` も並び、`prune-worktrees --hook` に hook 入力をそのまま渡す。
今開いたリポジトリの `.claude/worktrees/` 配下で、foreground セッションが keep したまま 1 週間触っていない
worktree を `git worktree remove` し、回収したときだけ `systemMessage` で知らせる。判定の条件は
`internal/worktree` の package doc、手で叩く形と sandbox の制約は [operations.md](operations.md) の
「放置された worktree を回収する」。state は書かない。
<!-- cc-premise: 2.1.302 worktrees 本体の sweep は subagent / background session の worktree だけを消し、foreground の keep は残す -->
subagent の worktree (`claude agent` の lock) は Claude Code 本体の periodic sweep (`cleanupPeriodDays`) が消すので
触らない。daemon の housekeeping にしなかったのは、daemon が rex の外では起きず、対象リポジトリの一覧も cwd も
持たないため (#97)。

plugin は mod も持つ。`hooks/hooks.json` の `modules` に書ける module は 1 つなので、`hooks/register.ts` が
`hooks/board.tsx`、`hooks/park.tsx`、`hooks/band.tsx` の hook を束ね、皆が使う `session.start` を持つ。`board` コマンドで
依存のボード (B) を会話の横 (dock) の pane に開き、`agents-daemon status --board --session <id> --cwd <cwd>` の JSON を
数秒ごとに読み直して描く。ボードは `internal/board` が `$STATE/tasks/` (PostToolUse hook の `ingest-task` が書く) と
`status.json` から組む。行は repo (`git rev-parse --git-common-dir` の main worktree)、列はタスクの状態で、
`blockedBy` に完了していないタスクがあればブロックの列に置く。セッションの pane が入力待ちか上限待ちなら、
その進行中のカードに印を付ける。描くのは開いたセッションの domain だけで、指示を送れるのはボードに載った生きている
セッションだけ。
<!-- cc-premise: 2.1.302 hooks mods の pane / session.send / session.cwd と、PostToolUse の Task 系の形 -->

B は fullscreen のレイアウトで dock に置かれるときだけ出す。`/board` は `presentation.isFullscreen` が false なら開かず、
開いた後に `placement: inline` で描かれたら (端末を 110 桁より狭めたときなど) mod が閉じる。中身は下の `graph` を
止まっているノードを根にした木にしたもので、節は `■` blocked (止まっているノードと待ちの輪)、`▸` working、
`↵` idle (done と、状態を分類できない `·` unknown)、`○` pending (止まっているノードの下流にいない、上流の無い pending)、`⊘` exited、`◇` parking の順。木の組み方と記号は
`hooks/graph.ts` が持つ。選んだノードの詳細で辺を操作する。

- `kind: edge` の辺は `Unlink` で `edge rm` に元の記録の参照を渡す
- `kind: task` の辺 (同じセッションの `blockedBy`) は daemon から外せず、TaskUpdate にも外す引数が無い。
  `Ask to unlink → <session>` でそのセッションへ、待たれている側を片付けるか待っている側を作り直すよう頼む
- `+ Add blocker` は足すと輪になる候補 (下流) を出さず、`edge add` に両端の参照と cwd を渡す
<!-- cc-premise: 2.1.303 mods pane の placement (dock / inline) と command.run の presentation.isFullscreen -->

パーキングロットに置くもの 1 つを、docs と CLI の文言では「メモ」と呼ぶ。コードでは `parking.Item` で、
`park add` の結果の `item` と `park list` の配列の要素がこれに当たる。識別子と JSON のキーは `item` のまま。

ボードの JSON は行ごとに open のメモ (`$STATE/parking/`) をタスクの列とは別の欄 `parking` に持ち、メモだけの repo も
行にする。メモの見せ方と操作は `hooks/parking.ts` が持ち、行と列の描き方には依存しない。操作はどれも
`agents-daemon park ... --session <id> --cwd <cwd>` を呼び、開いたセッションの domain のメモとタスクだけを触る。

- 捨てる: `park set <id> --status discarded`
- タスクにする: 同じ repo の生きているセッションへ TaskCreate を頼む文面を送る
- issue にするよう頼む: 私用のボードだけ。ボードに載っている生きているセッションへ頼む
- タスクにする・issue にするよう頼むは、`$.session.send` が届いたときだけ `park set <id> --status handed` にする
- パーキングへ移す (`To parking`): 終了したセッションの未完了のタスクのノードを `park adopt <session_id> <task_id>` で
  `kind: task` のメモにし、タスクの記録を消す。`kind: task` は model の tool からは置けない

ボードの JSON の `graph` はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つ (カードと同じ Key) で、
辺は同じセッションの `blockedBy` (`kind: task`) と `$STATE/edges/` の辺 (`kind: edge`) を 1 つにしたもの。
辺の記録の端点はボードを組むたびにノードへ解決し直す (`internal/board` の `graph.go`)。

- `task:<sid>/<tid>` はそのタスク。記録が消えていればその辺は描かない
- `session:<名前>` は、その名前のセッションのうち最後に動いたもの (pane があればそれ、無ければタスクの更新が新しいもの)。
  after 側は根のタスク (同じセッションの blockedBy が無いもの)、before 側は末端のタスク (同じセッションのどのタスクも
  止めていないもの) につなぐ。タスクの無い生きているセッションは `<sid>/session` のノード
- まだ起きていないセッションと `issue:` / `pr:` は参照を鍵にした置き場のノード (`ref:<参照>`、`pending`)。
  タスクを作らずに終了したセッションも置き場のノードにし、`exited` で下流が止まっていることを見せる

ノードの状態は、完了なら `completed`、pane の無いセッションの未完了は `exited`、生きている pane の未着手は `pending`、
進行中は pane の状態 (上限待ちを agent の申告より先に見る)。段・下流の数・待ちの輪・原因の鎖・最長の鎖の数え方は
`internal/dag` が持つ。
<!-- cc-premise: 2.1.302 osc7501 agent_status と blocked の kind (permission / question / auth) からノードの状態を決める -->

`hooks/park.tsx` は model が呼べる tool `mcp__agents-daemon__park` を登録する (パーキングロットの置く側)。
呼ばれたら `agents-daemon park add --cwd <cwd>` に、model が書いた欄とこのセッションの `session_id` を JSON で
stdin に渡す。`session_id` と cwd は inputSchema に無く、model は書けないので、domain の判定は `status --board` と
同じ入力で決まる。結果の `created` で「メモを置きました」と「既に置かれていたメモにまとめました」を分けて返す。
prompt の hint 行の末尾 (`PromptHint` の `tail`) に `park list --here --status open` の数 (このセッションの repo の
open のメモ) を出し、session.start、park の後、main loop の turn.complete で読み直す。全セッションで常に動くため、
clock での定期の読み直しはしない。`$.ui.status` はエンジンが警告の印付きで描くので使わない。`tail` を描くのは terminal だけ。
<!-- cc-premise: 2.1.302 hooks mods の tool.register / tool.call / turn.complete / ui.render PromptHint の tail -->
<!-- cc-premise: 2.1.302 hooks $.ui.status は「⚠ <plugin>: <text>」と警告の印付きで描かれる -->
<!-- cc-premise: 2.1.302 hooks modules は 1 つ、matcher の無い hook は event ごとに 1 つ -->
<!-- cc-premise: 2.1.302 hooks tool.register は local の port を開く (sandbox の中の claude -p では落ちる) -->

`hooks/band.tsx` は入力欄の上の band (`ui.render` の `AbovePrompt`) に、依存の DAG (A) とパーキングのメモの 1 行を
出す。band は 1 枚で、両方が分け合う。辺もメモも無ければ出さず、survey があるときは譲る。

- 1 行目: `■ ○ ▸ ✓ ◇` の数と `longest chain` (止まっているノードから下流の最長の鎖)、`Show completed` / `Hide completed`
- `earlier ────► later` の軸の下に、段 (前提の深さ) ごとの列でノードを並べる。完了は既定で畳み、残ったノードの段で詰める
- 最後の行: メモを 3 件まで (種類と title)。残りは `+n`
- ボードは `hooks/board-data.ts` で session.start、TaskUpdate の後、10 秒ごとに読み直す。B の pane を開いている間は
  B が 3 秒ごとに読み直し、band は同じ board を描く
- メモは `hooks/park.tsx` が読んだ一覧 (この repo の open のメモ) を新しく置かれた順に並べる。TaskUpdate の結果が
  `completed` になったら、`park list --status open --task <task_id> --session <id> --cwd <cwd>` (そのタスクの途中で
  このセッションが置いたメモ) を先頭に寄せる。どちらも `--session` で開いたセッションの domain のメモだけを引く
- band で選んだノードとメモ (`target`) は B の pane と共有し、B でも同じものを開く
<!-- cc-premise: 2.1.303 hooks mods の ui.render AbovePrompt (hasSurvey、bodyColumns) と、TaskUpdate の結果の statusChange -->

#### 時刻の書き方

人が読む表示 (ボードのメモの詳細) は、メモの時刻を JST の `MM/DD HH:MM` で出す。変換は
`hooks/parking.ts` の `stamp` が 1 箇所で持ち、記録の offset にも mod の時刻帯にも頼らない。

メモの id (`20261018T162844-53de0c9a`) の時刻は UTC にする。id はファイル名と `park set <id>` の鍵なので、端末の
時刻帯に左右されず、名前の並びが置いた順と一致する形にする。id の時刻は人に見せず、表示には `sources[].at` を使う。

記録に書く時刻 (`sources[].at`、タスクの記録、`status.json`、ログ) は offset 付きの RFC 3339 のままにし、
offset を揃えない。どの offset で書いても同じ瞬間に戻せるので、揃えるのは表示の側だけで足りる。

#### バイナリの用意と差し替え

plugin には install 時の lifecycle hook が無いので、バイナリも SessionStart hook が用意する。
置き場は `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`。plugin の install 先はバージョンごとに
変わるため、その外の固定パスにする。

| 状態 | `ensure-daemon.sh` の動き |
| ---- | ---- |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` があり、隣の `agents-daemon.version` が plugin.json の `version` と一致 | `daemon --ensure` を exec する |
| それ以外 (未ビルド、plugin の更新後) | `hooks/build-daemon.sh` を detach して即座に戻る |

`build-daemon.sh` は plugin の root で `go build ./cmd/agents-daemon` を回し、同一ディレクトリの
一時ファイルから `mv` でバイナリを差し替え、stamp を書いてから `daemon --ensure` を叩く。
出力は `$STATE/logs/build.log` に残る。SessionStart を `go build` で待たせないため、
初回のセッションでは daemon が build の完了後に起きる。

- `go` が PATH に無ければ `build.log` に `ERROR ... go not found in PATH` を残して終わる。
  hook は PATH を仮定しないので、Claude Code を起動するシェルの PATH に Go を入れる。
- 同時に起きた build は `bin/.build.lock` (ディレクトリ。中の `pid` が持ち主) で 1 本に絞る。
  持ち主が生きていれば後から来た側は何もせずに終わる。build が SIGKILL などで落ちて lock が残っても、
  次の build が持ち主の死を `kill -0` で確かめて残骸を消し、取り直す (`build.log` に `stale lock` が残る)。
  手で消す必要は無い。
- plugin の更新後にセッションを開き直さず入れ替えるには、install 済み plugin の `hooks/build-daemon.sh` を
  手で叩く。コマンドは [operations.md](operations.md) の「plugin の更新後に daemon を入れ替える」。
- 開発中は `task install` で同じパスへ建てられる。中身は `hooks/build-daemon.sh` そのもので、
  stamp には clone の plugin.json の version が書かれる。install 済み plugin と version が同じなら
  次の SessionStart でも建て直されず、clone の版が動き続ける。plugin 側の版へ戻すときは stamp を消す。

Go のバイナリは起動時にメモリへ読み込まれるので、ファイルを差し替えても
動作中のプロセスには反映されない。`--ensure` は「生きているデーモンがあれば何もしない」
ため、放っておくと直したはずのコードが何日も動かないままになる。

そこでデーモンは毎 tick で自分の実行ファイルの mtime とサイズを見て、差し替わっていたら
後始末をしてから新しい版を detach 起動して入れ替わる (`internal/daemon/selfupdate.go`)。
build の数秒後には新しい版が動いている。hook も手動操作も停止・再起動をしない。

判定できないとき (実行ファイルが一時的に見えない等) は「変わっていない」に倒す。
動いているデーモンを不用意に落とす方が害が大きいため。

### [3] デーモンが監視して再開する

単一プロセスが全 Claude pane を見る。pane ごとの状態は pane ID (rex の block ID) をキーに
プロセス内で持つ。

### 多重化ツールの backend

daemon は pane 操作を `internal/multiplexer` の `Client` interface 越しに行い、実装は rex (`internal/rexcli`)。
`internal/backend` が起動時の環境に `REX_SESSION` があることを確かめ、無ければ `daemon --ensure` は
stderr に 1 行出して何もせずに戻る (素の端末の SessionStart で毎回 daemon を起こさない)。hook はこの判定を
持たず、バイナリの build は常に行う。同じバイナリを `statusline` / `ingest-statusline` が使い、
これらは多重化ツールを要らないため。backend は `status.json` の `backend` に書き、`status` が表示する。

| 操作 | rex |
| ---- | ---- |
| PaneList | `ls --json`、session ごとに `block ls -s S --json`、flavor が `com.superlogical.terminal.shell` の block ごとに `block call -b B program_status` と `block call -b B process` |
| PaneRead | `capture -b B --trim --unwrap` の末尾 N 行 |
| SendText | `send -b B TEXT` |
| SendKeys | `send-key -b B KEY...` |
| Reachable | `api call server.status` の `pid` が正 |
| session_id | `context/<sid>.json` の `multiplexer.block` から逆引き (上の「statusline が書く state」) |

待機中のラベルは出さない。rex で表示に使える `block rename` は利用者が付けた label を潰すため。

rex の呼び出しには全部 `--autostart=false` を付け、子プロセスの環境から `REX_SESSION` / `REX_BLOCK` を外す。
`REX_SESSION` があると rex は block の解決をその session の中に閉じ、外せば完全 ID で別 session の block も
解決する (実測)。`block ls` と `block call` の間に閉じた block (exit 3) は読み飛ばし、PaneList の失敗に数えない。

rex の列挙は 1 tick に `ls` 1 回、session ごとに `block ls` 1 回、shell の block ごとに 2 回の fork になる。
block が多い環境ほど tick あたりの時間が伸びる。

<!-- rex-premise: preview.15 cli block の PATH は Versions/<build>/… で、更新で旧 build のディレクトリが消える -->

rex のバイナリは最初の呼び出しで PATH から絶対パスに引いて覚える (`REX_BIN_PATH` があればそれで固定)。
rex の自動更新は `…/RexServer/Versions/<build>/` を入れ替えて旧 build のディレクトリを消すので、
daemon が hook から継いだ PATH の項は指す先を失う。呼び出しがバイナリ無しで落ちたら、PATH の
`Versions/<build>/…` の項から一番新しい build の rex を引き直して同じ呼び出しを 1 回やり直し、tick の失敗に数えない。
`REX_BIN_PATH` で固定しているときは引き直さない (`internal/rexcli/locate.go`)。

rex で Agent と AgentStatus を決めるのは Claude Code 自身が OSC 7501 で報告する `program_status` で、
状態の対応と record を使う条件は [program-status.md](program-status.md)。

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
| `$STATE/cost/<YYYY-MM-DD>/<session_id>.json` | `agents-daemon statusline` (`CLAUDE_STATUSLINE_SPEND_CAP` があるとき) | statusline の描画ごと (累計が変わったときだけ書き直す) | そのセッションの累計 cost と前日までの繰り越し (`internal/spend`)。daemon は読まない | `statusline` がその日の最初の書き込みで 45 日より古い日を消す |
| `$STATE/tasks/<domain>/<session_id>/<task_id>.json` と `…/todos.json` | `agents-daemon ingest-task` (PostToolUse hook `hooks/ingest-task.sh` が呼ぶ) | TaskCreate / TaskUpdate / TaskList / TodoWrite の呼び出しごと | タスク 1 つ (subject、status、blockedBy、session_id、cwd) と TodoWrite のリスト全体 (`internal/tasklog`)。`<domain>` は `home` か `work` (config の `homePaths` / `defaultDomain`)。読み手はタスクのボード (`status --board`、`internal/board`) | daemon が完了したタスクを 1 日、それ以外を 7 日 (更新が無ければ) で消す。`park adopt` (ボードの「パーキングへ移す」) がメモへ移したタスクを消す |
| `$STATE/parking/<domain>/<id>.json` | `agents-daemon park add` / `park set` / `park adopt` | メモを置いたとき、status を書き換えたとき、終了したセッションのタスクをメモへ移したとき (kind `task`) | パーキングのメモ 1 つ (kind、title、detail、files、default_taken、repo、sources (session_id、置いたときの進行中の task_id、cwd、まとめたときに後から置いた側の detail、時刻)、status) (`internal/parking`)。repo はボードの行と同じ main worktree のパス。domain、repo、正規化した title が同じ open のメモには sources を足す。`.lock` は書き込みを直列にするロック | daemon が open を 90 日、handed / discarded を 7 日 (更新が無ければ) で消す。同じメモが置き直されると数え直す。`.lock` は消さない |
| `$STATE/edges/<domain>/<id>.json` | `agents-daemon edge add` / `edge rm` (作業を渡す側の skill が呼ぶ) | 辺を書いたとき、消したとき | セッションをまたぐ辺 1 本 (before と after の端点 (参照、cwd、repo)、note、created_at) (`internal/edge`)。辺は「before が済まないと after を始められない」。id は向きを含めた両端の参照から決まり、同じ辺の登録し直しは同じファイルを書き直す。両端の domain が違う辺は書かない。読み手は `edge list` とボードの DAG (`status --board` の `graph`) | daemon が 7 日 (更新が無ければ) で消す。登録し直すと数え直す。`edge rm` が消す |
| `$STATE/daemon.pid` | daemon | 起動時に作成、終了時に削除 | 稼働中デーモンの PID | daemon |
| `$STATE/status.json` | daemon | 毎 tick (既定 5 秒) | backend (rex)、監視中の pane 一覧と各 pane の自己申告 (`agent_status` と、あれば `agent_kind` / `agent_message`)・監視状態・試行回数・待機期限・直近の判定 | 上書き |
| `$STATE/logs/daemon.log` | daemon | 報告に値する出来事があったときだけ | 上限検知と待ち時間、再開送信、ユーザーの自己再開、ゲート抑制、ネイティブへの譲り、エラー | rotate |
| `$STATE/logs/daemon.log.YYYY-MM-DD` | daemon | 日付が変わって最初に書くとき | 前日までの `daemon.log` をそのまま退避したもの | daemon が 7 日で消す |
| `$STATE/limit-events/<YYYY-MM-DD>.jsonl` | daemon | 上限の出来事 (検知して待機に入った、送った、抑制した、ネイティブに譲った、Enter 待ち、hard cap、prompt で見送り) で daemon.log に行を書くのと同じ遷移のとき | 1 出来事 1 行の JSON (`internal/limitlog`): 判定した画面そのもの (末尾 `readLines` 行)、分類 (kind)、outcome、Claude Code の自己申告 (record)、block の高さと screen (`size` / `modes`)、own と account の利用上限 state、`readLines` / `detectionTailLines`。読み方は operations.md「次に上限を踏んだとき」 | daemon が 30 日で消す |
| `$STATE/logs/board-<domain>.log` | `agents-daemon ingest-task` / `park add` / `park set` / `park adopt` と、mod (`hooks/log.ts`) が呼ぶ `agents-daemon board-log` | タスクの記録、メモの操作、mod の出来事 (tool の登録の失敗、ボードの数と読み込みの失敗、送信の結果) があったとき。mod は同じ失敗と同じ数を書き直さない | ボードの経路の出来事を 1 行 1 件 (`internal/boardlog`、出来事の一覧は package doc)。session ID、task ID、メモの id、件数、失敗の理由だけで、subject、title、detail、送った文面は書かない。`<domain>` で分け、業務の出来事を私用の側に出さない。`logs --board` が読む | rotate |
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
