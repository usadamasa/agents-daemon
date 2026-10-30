# 依存図 (mod)

plugin の mod が描く依存図の pane と、model の `SendMessage` の結果に添える辺の書き方の通知。パーキングのメモの
見せ方と操作、park tool、ファイルのメモの通知は [parking.md](parking.md) が持つ。mod は Claude Code の中で動き、
データはバイナリのサブコマンド越しに読み書きする。

## mod の束ね方

`hooks/hooks.json` の `modules` に書ける module は 1 つなので、`hooks/register.ts` が
`hooks/deps.tsx`、`hooks/park.tsx`、`hooks/notice.ts`、`hooks/handoff-notice.ts` の hook を束ね、皆が使う
`session.start` を持つ。

## 依存図の pane

`/deps` コマンドで依存図を会話の横 (dock) の pane に開き、`agents-daemon deps --cwd <cwd>` の JSON を数秒ごとに
読み直して描く。読み込みは `hooks/deps-data.ts` の `loadDeps` が 1 箇所で持つ。JSON は `deps` パッケージが
`$STATE/tasks/` (PostToolUse hook の `ingest-task` が書く)、`status.json`、`$STATE/edges/`、`$STATE/parking/` から組む。

- `graph` はタスクの依存の DAG (次の節)
- `repos` は repo (`git rev-parse --git-common-dir` の main worktree) ごとの生きているセッション (`sessions`) と
  open のメモ (`memos`)。セッションは作業の cwd の repo に入り、タスクの無いセッションは pane の cwd の repo に入る。
  メモだけの repo も載る。並びは見出し、鍵の順
- `sessions` の `subject` は、その repo で進行中のタスク (Key の順で最初のもの)、進行中の TodoWrite の項目、
  pane の今の作業 (`agent_message`)、セッション名、cwd の末尾の順に選ぶ。並びは表示名、session ID の順
- `here` は開いたセッションの repo の鍵で、pane はこの repo を自セッションの repo として扱う

指示を送れるのは生きているセッションのノードだけ。
<!-- cc-premise: 2.1.302 hooks mods の pane / session.send / session.cwd と、PostToolUse の Task 系の形 -->

pane は fullscreen のレイアウトで dock に置かれるときだけ出す。
`/deps` は `presentation.isFullscreen` が false なら開かず、
開いた後に `placement: inline` で描かれたら (端末を 110 桁より狭めたときなど) mod が閉じる。
中身は「依存の DAG」の `rows`: 1 行 1 ノードで上流を上に並べ、行頭に辺のレーンを罫線で描いたもの
(`git log --graph` の形)。合流は `├─┘`、分岐は `├─┐` の行を挟み、
閉じたレーンには右のレーンを 1 本ずつ `┌─┘` の行で詰める。セッションをまたぐ辺 (`kind: edge`) は `┆ ┄` の破線、
longest chain の辺の罫線 (`marks`) は色付き。同じノードは 1 回しか出ず、
先頭の数の行 (`■ ○ ▸ ↵ ✓ ◇`) の下に全部の行を描く (pane はスクロールする)。完了したノードは既定で出さず、
`Show completed` で `rows_with_completed` に切り替える。pane の底には記号の凡例を薄く置き、中身が pane の高さに
満たないときも底に寄せる。記号と数の行と凡例は `hooks/graph.ts`、レーンの割り当ては `graph/dag` が持つ。
選んだノードの詳細で辺を操作する。

- `kind: edge` の辺は `Unlink` で `edge rm` に元の記録の今の参照を渡す (`sid:` の端点がタスクへ解決された後の
  ノードの key は辺の記録に無いので、それでは消せない)
- `kind: task` の辺 (同じセッションの `blockedBy`) は daemon から外せず、TaskUpdate にも外す引数が無い。
  `Ask to unlink → <session>` でそのセッションへ、待たれている側を片付けるか待っている側を作り直すよう頼む
  (タスクの ID はノードの `task_id`)
- `+ Add blocker` は足すと輪になる候補 (下流) と `endpoint` が false のノードを出さず、`edge add` に両端のノードの
  key と cwd を渡す

詳細の操作は枠の最後の行の `▾` で開くメニューに 1 行ずつ並べ、選んだらメニューを閉じてすぐ走らせる (#308)。
横に並べると pane の幅で見切れるため。`Select` は開いた一覧を後ろの行に重ねて描き、枠の罫線と凡例が崩れるので
使わず、枠なしの `Button` で並べて後ろの行を押し下げる。宛先を選ぶ操作 (`Hand off`、`To task`、`Ask for issue`) は
見出しの下に宛先を `├` / `└` の枝で並べる。
<!-- cc-premise: 2.1.312 mods Select は開いた一覧を後ろの行に重ねて描き、場所を取らない -->

開いたセッション自身の完了していないタスクには、`Start` / `Complete` / `Hand off` (宛先) / `Delete` / `To parking` /
`+ Add blocker` を出す (`hooks/own-task.ts`)。mod は TaskUpdate を呼べないので、どの操作も自分の入力
(`$.prompt.submit`) へ頼む文を渡し、タスクを動かすのは自分の model に任せる。
peer への `Hand off` だけは mod が peer へ `$.session.send` で session ID 宛てに title とノードの `description` を送り、
届いてから自分にタスクを消すよう頼む。ListAgents の名前は依存図のどの欄 (表示名、`session_name`、session ID) とも
一致しないことがあり、model に `SendMessage` で渡させると宛先を引き当てられない。渡す先は repo を問わず生きている
ほかのセッション。宛先の `new session` は起こしたセッションに自分が名前を付けるので、`agents-daemon:peer-sessions`
skill の手順で渡すよう自分に頼む。`▾` の右の補足欄 (`✎`) に書いた文は、`Start` と `Hand off` の文面 (peer へは相手へ送る文面) の
末尾に `利用者からの補足: …` として足し、選んだら欄を空にする (#310)。空なら文面は変わらない。`Complete` / `Delete` /
`To parking` には足さない。開いたメニューと補足の下書きはノードを選び直すと消える。`To parking` は、扱うメモ (`memos` のうち捨てていないもの)
があれば新しいメモを作らず、handed のメモを `park set --status open --from handed` で戻してから消すよう頼み、
入力へ渡せなければ自分への handed (`--to-session <自分>`) に取り直す。扱うメモが無ければ park tool で置いてから
消すよう頼む。
<!-- cc-premise: 2.1.303 mods pane の placement (dock / inline) と command.run の presentation.isFullscreen -->

pane を開いたとき、数 (repo、セッション、ノード、メモ) が変わったとき、`deps` が失敗したときは出来事のログに
`mod.deps.open` / `mod.deps.changed` / `mod.deps.failed` を書く (`hooks/log.ts`、`eventlog`)。

## 依存の DAG (`graph`)

`graph` はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つ (`task:<sid>/<tid>`)、TodoWrite の
項目 1 つ (`todo:<sid>/<n>`)、タスクの無い生きているセッション 1 つ (`sid:<sid>`) で、辺は同じセッションの
`blockedBy` (`kind: task`) と `$STATE/edges/` の辺 (`kind: edge`) を 1 つにしたもの。ノードの `key` は Ref
(`ref`) そのもので、辺の端点と同じ形になる。mod は key を切り出さず、端点にできるかは `endpoint`
(TodoWrite の項目だけ false)、タスクの ID は `task_id` で読む。ノードの材料 (`graph.Card`) は `deps` パッケージが組み、
辺の記録の端点は依存図を組むたびにノードへ解決し直す (`deps/graph`)。

- `task:<sid>/<tid>` はそのタスク。記録が消えていればその辺は描かない
- `sid:<sid>` はそのセッション。after 側は根のタスク (同じセッションの blockedBy が無いもの)、before 側は
  末端のタスク (同じセッションのどのタスクも止めていないもの) につなぐ。タスクの無い生きているセッションは
  `sid:<sid>` のノード
- `session:<名前>` は daemon がまだ結びつけていない端点で、同じ名前のセッションが生きていても名前を key にした
  置き場のノード (`pending`) にする。読むたびに名前から引き直さないので、同名のセッションを起こし直しても辺の意味は
  変わらない
- `issue:` / `pr:` も参照を key にした置き場のノード (`pending`)。タスクを作らずに終了したセッションは `sid:<sid>` を
  key にした置き場のノードにし、`exited` で下流が止まっていることを見せる

`session:<名前>` の端点は、daemon が tick ごとに status.json を書いた直後、セッション台帳 (`$STATE/sessions/`、
`daemon/ledger`) を書いてから結びつける (`edge` の `Bind`)。候補は名前が一致し、辺の
created_at に生きていたか後から現れた (last_seen が created_at 以後の) セッション (`ledger.Pick`)。辺を書いた時点で
動いていた候補 (first_seen が created_at 以前) があれば first_seen の最も新しいもの、無ければ created_at より後に
最初に現れたものを選ぶ。渡す側は `claude --name` で起こしてすぐ `edge add` を書くので、直前に起きた同名のセッションが
相手になる。古い同名のセッションを待たせたかった場合のような取り違えは、`edge rm` してから `sid:` で書き直す。結びつけた端点は `sid:<sid>` に書き換え、元の参照を `bound_from` に残し、二度と結び直さない。
tick ごとにするのは、渡した側が `edge add` を書いてから数秒で端点がそのセッションのノードにつながり、pane が置き場の
ノードを描き続けないようにするため。結びつく前の数秒は、`hooks/graph.ts` の `unlinked` が記録の `session:<名前>`
で辺を見つけるので、辺の書き忘れの通知は出ない。

辺の id は書いたときの両端の参照から決め、結びつけても変わらない (`edges/<id>.json` のまま)。`edge add` と
`edge rm` は、書いたときの参照の組の id のファイルを先に見て、無ければ今の参照の組が同じ辺を探すので、どちらの組でも
同じ辺に当たる。`edge add` は結びついた端点を残して note と cwd だけを書き直す。結びつけると今の参照の組が既にある
辺と重なるときは、同じ依存なので結びつける側の辺を消して 1 本にする。`Add`、`Remove`、`Bind`、`Prune` は
`edges/.lock` を取って読んでから書く。ロックが無いと、`edge add` が読んだ後に結びついた端点を名前に戻し
(`BindOnce` の反例)、`Bind` が読んだ後に `edge rm` で消した辺を作り直す (`NoResurrect` の反例)。手順の交錯は
`models/handoff/EdgeBind.tla`、実物の Store での交錯は `edge/bind_invariants_test.go` が確かめる。

pane を解釈するのは `deps` パッケージの `activity` だけで、生きているセッションごとに 1 回、セッションの活動
(`working` / `done` / `question` / `permission` / `error` / `limit` / `unknown`) を導く。上限待ちを agent の申告より
先に見て、`limit` のときは解除時刻 (`15:04`) を `detail` に添える。ノードの状態は作業の進み、セッションの生死、活動だけで
決まる純粋な関数 (`deps/graph` の `nodeState`): 完了なら `completed`、生きていないセッションの未完了は `exited`、
生きているセッションの未着手は `pending`、進行中は活動そのもの。
<!-- cc-premise: 2.1.302 osc7501 agent_status と blocked の kind (permission / question / auth) からセッションの活動を決める -->

止まっているかもバイナリが決めて渡し、mod は状態の語から決めない。`halted` はセッション自身が止まっている
(`question` / `permission` / `limit` / `error`) ノードで、行の title の色と原因の鎖で先に選ぶ上流に使う。`stuck` は
`halted` か、完了していない待ちの輪のノードで、数の行の ■、行の末尾の下流の数、longest chain の起点に使う。

段・下流の数・待ちの輪・原因の鎖・最長の鎖の数え方と、`rows` / `rows_with_completed` (位相順の行とレーンの割り当て。
行の並びは `Nodes` の順を優先の順にし、直前のノードの下流で上流が出揃ったものを先に出す) は `graph/dag` が持つ。

## 渡した宛先の辺の確かめ

`hooks/handoff-notice.ts` は main の model の `SendMessage` (文面のあるもの) が成功したら、
`deps --cwd <cwd>` (`hooks/deps-data.ts` の `loadDeps`) を読み、宛先 (末尾の ` [ref]` は落とす) が
依存図に生きていて済んでいないノードを持つセッションで、`kind: edge` の辺が端点 (そのセッションのタスクへ解決された
端点か、記録の `session:<名前>` の参照) に 1 本も無ければ (`hooks/graph.ts` の `unlinked`)、tool の結果の `context` に
辺の書き方を 1 つ添える (#172)。文面は
`agents-daemon edge add --before … --after session:<名前> --after-cwd <宛先の cwd>` と
`agents-daemon:peer-sessions` skill の「依存の辺を書く」を指し、返事や報告なら辺は要らないと添える。

- 辺を書くのは渡した側の model だけなので、人ではなく model に伝える
- subagent や teammate、cloud のセッションへの送信は依存図の名前に当たらないので添えない
- mod 自身の送信 (`$.session.send`、メモの手渡しやノードへの指示) は `tool.call` を通らないので見ない
- 送信のエラー、subagent の送信、文面の無い送信 (`notify_when_idle` だけ、shutdown などの要求)、依存図の読み込みの
  失敗では何も添えない
<!-- cc-premise: 2.1.310 mods SendMessage の tool.call は e.to と e.message を持ち、context は model だけが読む -->
