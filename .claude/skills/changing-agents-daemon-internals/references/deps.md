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
読み直して描く。読み込みは `hooks/deps-data.ts` の `loadDeps` が 1 箇所で持つ。JSON は `internal/deps` が
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
満たないときも底に寄せる。記号と数の行と凡例は `hooks/graph.ts`、レーンの割り当ては `internal/deps/dag` が持つ。
選んだノードの詳細で辺を操作する。

- `kind: edge` の辺は `Unlink` で `edge rm` に元の記録の参照を渡す (辺の id は記録の参照の組から決まるので、
  解決した後のノードの key では消せない)
- `kind: task` の辺 (同じセッションの `blockedBy`) は daemon から外せず、TaskUpdate にも外す引数が無い。
  `Ask to unlink → <session>` でそのセッションへ、待たれている側を片付けるか待っている側を作り直すよう頼む
  (タスクの ID はノードの `task_id`)
- `+ Add blocker` は足すと輪になる候補 (下流) と `endpoint` が false のノードを出さず、`edge add` に両端のノードの
  key と cwd を渡す
<!-- cc-premise: 2.1.303 mods pane の placement (dock / inline) と command.run の presentation.isFullscreen -->

pane を開いたとき、数 (repo、セッション、ノード、メモ) が変わったとき、`deps` が失敗したときは出来事のログに
`mod.deps.open` / `mod.deps.changed` / `mod.deps.failed` を書く (`hooks/log.ts`、`internal/eventlog`)。

## 依存の DAG (`graph`)

`graph` はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つ (`task:<sid>/<tid>`)、TodoWrite の
項目 1 つ (`todo:<sid>/<n>`)、タスクの無い生きているセッション 1 つ (`sid:<sid>`) で、辺は同じセッションの
`blockedBy` (`kind: task`) と `$STATE/edges/` の辺 (`kind: edge`) を 1 つにしたもの。ノードの `key` は Ref
(`internal/ref`) そのもので、辺の端点と同じ形になる。mod は key を切り出さず、端点にできるかは `endpoint`
(TodoWrite の項目だけ false)、タスクの ID は `task_id` で読む。ノードの材料 (`graph.Card`) は `internal/deps` が組み、
辺の記録の端点は依存図を組むたびにノードへ解決し直す (`internal/deps/graph`)。

- `task:<sid>/<tid>` はそのタスク。記録が消えていればその辺は描かない
- `session:<名前>` は、その名前のセッションのうち最後に動いたもの (pane があればそれ、無ければタスクの更新が新しいもの)。
  `sid:<sid>` はそのセッション。どちらも after 側は根のタスク (同じセッションの blockedBy が無いもの)、before 側は
  末端のタスク (同じセッションのどのタスクも止めていないもの) につなぐ。タスクの無い生きているセッションは
  `sid:<sid>` のノード
- まだ起きていないセッションと `issue:` / `pr:` は参照を key にした置き場のノード (`pending`)。タスクを作らずに
  終了したセッションは `sid:<sid>` を key にした置き場のノードにし、`exited` で下流が止まっていることを見せる。
  名前と session ID のどちらで書いた辺も同じ置き場に集まる

ノードの状態は、完了なら `completed`、pane の無いセッションの未完了は `exited`、生きている pane の未着手は `pending`、
進行中は pane の状態 (上限待ちを agent の申告より先に見る)。段・下流の数・待ちの輪・原因の鎖・最長の鎖の数え方と、
`rows` / `rows_with_completed` (位相順の行とレーンの割り当て。行の並びは `Nodes` の順を優先の順にし、直前のノードの
下流で上流が出揃ったものを先に出す) は `internal/deps/dag` が持つ。
<!-- cc-premise: 2.1.302 osc7501 agent_status と blocked の kind (permission / question / auth) からノードの状態を決める -->

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
