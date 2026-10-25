# ボードとパーキング (mod)

plugin の mod が描く依存のボード (B)、入力欄の上の band (A)、パーキングのメモの見せ方と操作、model が呼ぶ park tool。
mod は Claude Code の中で動き、データはバイナリのサブコマンド越しに読み書きする。

## mod の束ね方

`hooks/hooks.json` の `modules` に書ける module は 1 つなので、`hooks/register.ts` が
`hooks/board.tsx`、`hooks/park.tsx`、`hooks/band.tsx` の hook を束ね、皆が使う `session.start` を持つ。

## 依存のボード (B)

`board` コマンドで依存のボード (B) を会話の横 (dock) の pane に開き、
`agents-daemon status --board --session <id> --cwd <cwd>` の JSON を数秒ごとに読み直して描く。
ボードは `internal/board` が `$STATE/tasks/` (PostToolUse hook の `ingest-task` が書く) と `status.json` から組む。
行は repo (`git rev-parse --git-common-dir` の main worktree)、列はタスクの状態で、
`blockedBy` に完了していないタスクがあればブロックの列に置く。セッションの pane が入力待ちか上限待ちなら、
その進行中のカードに印を付ける。描くのは開いたセッションの domain だけで、指示を送れるのはボードに載った生きている
セッションだけ。
<!-- cc-premise: 2.1.302 hooks mods の pane / session.send / session.cwd と、PostToolUse の Task 系の形 -->

B は fullscreen のレイアウトで dock に置かれるときだけ出す。
`/board` は `presentation.isFullscreen` が false なら開かず、
開いた後に `placement: inline` で描かれたら (端末を 110 桁より狭めたときなど) mod が閉じる。
中身は「依存の DAG」の `rows`: 1 行 1 ノードで上流を上に並べ、行頭に辺のレーンを罫線で描いたもの
(`git log --graph` の形)。合流は `├─┘`、分岐は `├─┐` の行を挟み、
閉じたレーンには右のレーンを 1 本ずつ `┌─┘` の行で詰める。セッションをまたぐ辺 (`kind: edge`) は `┆ ┄` の破線、
longest chain の辺の罫線 (`marks`) は色付き。同じノードは 1 回しか出ず、
先頭の数の行 (`■ ○ ▸ ↵ ✓ ◇`) の下に全部の行を描く (pane はスクロールする)。完了したノードは既定で出さず、
`Show completed` で `rows_with_completed` に切り替える。pane の底には記号の凡例を薄く置き、中身が pane の高さに
満たないときも底に寄せる。記号と数の行と凡例は `hooks/graph.ts`、レーンの割り当ては `internal/dag` が持つ。
選んだノードの詳細で辺を操作する。

- `kind: edge` の辺は `Unlink` で `edge rm` に元の記録の参照を渡す
- `kind: task` の辺 (同じセッションの `blockedBy`) は daemon から外せず、TaskUpdate にも外す引数が無い。
  `Ask to unlink → <session>` でそのセッションへ、待たれている側を片付けるか待っている側を作り直すよう頼む
- `+ Add blocker` は足すと輪になる候補 (下流) を出さず、`edge add` に両端の参照と cwd を渡す
<!-- cc-premise: 2.1.303 mods pane の placement (dock / inline) と command.run の presentation.isFullscreen -->

## 依存の DAG (`graph`)

ボードの JSON の `graph` はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つ (カードと同じ Key) で、
辺は同じセッションの `blockedBy` (`kind: task`) と `$STATE/edges/` の辺 (`kind: edge`) を 1 つにしたもの。
辺の記録の端点はボードを組むたびにノードへ解決し直す (`internal/board/graph`)。

- `task:<sid>/<tid>` はそのタスク。記録が消えていればその辺は描かない
- `session:<名前>` は、その名前のセッションのうち最後に動いたもの (pane があればそれ、無ければタスクの更新が新しいもの)。
  after 側は根のタスク (同じセッションの blockedBy が無いもの)、before 側は末端のタスク (同じセッションのどのタスクも
  止めていないもの) につなぐ。タスクの無い生きているセッションは `<sid>/session` のノード
- まだ起きていないセッションと `issue:` / `pr:` は参照を鍵にした置き場のノード (`ref:<参照>`、`pending`)。
  タスクを作らずに終了したセッションも置き場のノードにし、`exited` で下流が止まっていることを見せる

ノードの状態は、完了なら `completed`、pane の無いセッションの未完了は `exited`、生きている pane の未着手は `pending`、
進行中は pane の状態 (上限待ちを agent の申告より先に見る)。段・下流の数・待ちの輪・原因の鎖・最長の鎖の数え方と、
`rows` / `rows_with_completed` (位相順の行とレーンの割り当て。行の並びは `Nodes` の順を優先の順にし、直前のノードの
下流で上流が出揃ったものを先に出す) は `internal/dag` が持つ。
<!-- cc-premise: 2.1.302 osc7501 agent_status と blocked の kind (permission / question / auth) からノードの状態を決める -->

## パーキングのメモ

パーキングロットに置くもの 1 つを、docs と CLI の文言では「メモ」と呼ぶ。コードでは `parking.Item` で、
`park add` の結果の `item` と `park list` の配列の要素がこれに当たる。識別子と JSON のキーは `item` のまま。

ボードの JSON は行ごとに open のメモ (`$STATE/parking/`) をタスクの列とは別の欄 `parking` に持ち、メモだけの repo も
行にする。`here` は開いたセッションの行の鍵で、mod はこの行の `parking` を band のメモにする。

### メモとタスクの照合

メモの側にはタスクを書き戻さない (#213)。ボードを組むたびに、タスクの記録の description から
メモの id (`internal/parking` の `Mentioned`、`newID` と同じ形) を拾い、status を問わず domain のメモと照合して、
そのタスクのノードの `memos` (id、title、status。description に書かれた順) に付ける。mod は選んだタスクの詳細に
`◇ <title> (<status>)` で出す。handed のメモはパーキングの列から消え、渡した先のタスクの詳細に現れる。
handed / discarded のメモが保持期間 (7 日) で消えた後は、description に id が残っていても照合できず、出さない。

### メモの見せ方と操作

メモの見せ方と操作は `hooks/parking.ts` が持ち、行と列の描き方には依存しない。メモの行は最後に置かれた時刻 (JST) を
先頭に出し、どのメモが古いかを列で見比べられるようにする。操作はどれも
`agents-daemon park ... --session <id> --cwd <cwd>` を呼び、開いたセッションの domain のメモとタスクだけを触る。

- 捨てる: `park set <id> --status discarded`
- 古いメモをまとめて捨てる (`Discard N older than 30d`): 最後に置かれてから 30 日より経った open のメモがあるときだけ
  パーキングの見出しに出し、`park discard --days 30` でその domain のメモをまとめて discarded にする。30 日は mod
  (`hooks/board.tsx` の `STALE_DAYS`) が持ち、CLI に既定は無い。housekeeping が open を 90 日で黙って消す前に、人が見て
  捨てる区切り。捨てたメモは 7 日残り、`logs --board` の `park.discard` に残る id を `park set <id> --status open` で戻せる
- タスクにする: メモの行が開いたセッションの repo (`here`) なら自セッション (`このセッション`) を先に、続けて同じ repo の
  生きているほかのセッションへ TaskCreate を頼む文面を送る。文面は着手の時期を指示せず受け手の計画 (タスクの依存) に
  任せ、タスクの description にメモの id を入れるよう頼む (既にこのメモを扱うタスクがあれば書き足すだけ)。
  照合の規則は「メモとタスクの照合」
- issue にするよう頼む: 私用のボードだけ。自セッションを先に、ボードに載っている生きているセッションへ頼む
- 自セッションは生きているのが自明なので、カードや daemon の有無にかかわらず宛先に出す。自分へは peer メッセージを
  送れないので `$.prompt.submit` で自分の入力へ渡し、idle になってから 1 ターンとして入る (#190)
- 新しいセッションを起こして渡す (`To task → new session…`、私用なら `Ask for issue → new session…`): 生きているセッションの
  無い repo へ渡す経路 (#162)。押すと repo の候補 (メモの行の repo、開いたセッションの repo、ボードのほかの行の repo) と
  絶対パスの入力欄を出し、選んだ repo で `agents-daemon spawn --repo <path> --session <id> --cwd <cwd>` に本文を stdin で
  渡す。バイナリが `claude --session-id <uuid> --model $CLAUDE_CODE_SUBAGENT_MODEL -- <本文>` を repo を cwd にした
  rex の window に起こし (`internal/spawn`、フォーカスは奪わず、終了後も pane を残す)、claude が前景になってから
  `{session_id, pane_id, foreground}` を返す。待ち切れたときは `foreground: false` と `detail` で返し、mod はメモを
  handed のまま残して pane を知らせる (起きたか分からないメモを戻すと、押し直しで 2 つ目が起きる)。本文は最初の
  ターンとして入るので、peer メッセージの inbox を待たない。文面は起こした
  経緯の 1 行に続けて peer へ送るものと同じで、issue はメモの repo ではなく起こした repo を起票先にする。repo の
  domain が開いたセッションの domain と違えば起こさない。モデルを subagent 用に揃えるのは peer-sessions skill と同じ
- タスクにする・issue にするよう頼むは、送る前に `park set <id> --status handed --from open` でメモを取る (今の status が
  open でなければ失敗し、送らない。2 つのセッションが同じメモを渡そうとしても取れるのは 1 つ)。`$.session.send` が
  届かなかった、`$.prompt.submit` が drop された、`spawn` が window を作れなかったときは `--status open --from handed` で
  戻す。Discard も `--from open` で、取られたメモは捨てない。手順の交錯で破れない性質は `models/handoff` が持ち、
  実装に対しては `internal/parking` と `internal/tasklog` の `invariants_test.go` が実物の Store の交錯で確かめる
<!-- cc-premise: 2.1.306 mods $.prompt.submit は idle になってから自分のターンとして入り、drop で入らなかったことが分かる -->
<!-- cc-premise: 2.1.306 cli claude [prompt] は最初のターンの本文、--session-id <uuid> でセッション ID を固定できる -->
- パーキングへ移す (`To parking`): 終了したセッションの未完了のタスクのノードを `park adopt <session_id> <task_id>` で
  `kind: task` のメモにし、タスクの記録を消す。`kind: task` は model の tool からは置けない。ボードは daemon が
  status.json を書いているとき (`updated_at` があるとき) だけこの操作を出し、バイナリは動いている daemon がタスクの
  記録より後に書いた status.json に pane が無いことを確かめてから移す (`internal/verify` の `SessionExited`)。
  daemon が止まっていると生きているセッションも exited と描かれるため

### 時刻の書き方

人が読む表示 (ボードのメモの行と詳細) は、メモの時刻を JST の `MM/DD HH:MM` で出す。変換は
`hooks/parking.ts` の `stamp` が 1 箇所で持ち、記録の offset にも mod の時刻帯にも頼らない。

メモの id (`20261018T162844-53de0c9a`) の時刻は UTC にする。id はファイル名と `park set <id>` の鍵なので、端末の
時刻帯に左右されず、名前の並びが置いた順と一致する形にする。id の時刻は人に見せず、表示には `sources[].at` を使う。

記録に書く時刻 (`sources[].at`、タスクの記録、`status.json`、ログ) は offset 付きの RFC 3339 のままにし、
offset を揃えない。どの offset で書いても同じ瞬間に戻せるので、揃えるのは表示の側だけで足りる。

## park tool

`hooks/park.tsx` は model が呼べる tool `mcp__agents-daemon__park` を登録する (パーキングロットの置く側)。
呼ばれたら `agents-daemon park add --cwd <cwd>` に、model が書いた欄とこのセッションの `session_id` を JSON で
stdin に渡す。`session_id` と cwd は inputSchema に無く、model は書けないので、domain の判定は `status --board` と
同じ入力で決まる。結果の `created` で「メモを置きました」と「既に置かれていたメモにまとめました」を分けて返す。
置いた後は `hooks/board-data.ts` がボードを読み直し、band のメモをすぐ変える。
<!-- cc-premise: 2.1.302 hooks mods の tool.register / tool.call -->
<!-- cc-premise: 2.1.302 hooks modules は 1 つ、matcher の無い hook は event ごとに 1 つ -->
<!-- cc-premise: 2.1.302 hooks tool.register は local の port を開く (sandbox の中の claude -p では落ちる) -->

## band (A)

`hooks/band.tsx` は入力欄の上の band (`ui.render` の `AbovePrompt`) に、依存の数の 1 行 (A) とパーキングのメモの 1 行を
出す。band は 1 枚で、両方が分け合う。辺もメモも無ければ出さず、survey があるときは譲る。図は描かない (辺を読んで
操作する面は B の pane に 1 つ)。

- 1 行目: `■ ○ ▸ ↵ ✓ ◇` の数と `longest chain` (止まっているノードから下流の最長の鎖)。選べるものは置かず、
  止まっているものがあると分かれば `/board` を開く合図にする
- `no edge: <名前>…` の行: model が `SendMessage` で送った宛先 (mods の `session.send`、`origin.kind: model`、届いたもの。
  末尾の ` [ref]` は落とす) のうち、ボードに生きていて済んでいないノードを持つセッションで、`kind: edge` の辺が端点
  (そのセッションのタスクへ解決された端点か、記録の `session:<名前>` の参照) に 1 本も無いもの (#172)。渡す側が辺を書く
  手順は `agents-daemon:peer-sessions` skill にあり、この行は書き忘れの合図。辺が書かれるかそのセッションが済むと消え、
  依存の無い送信なら名前を押して外す。subagent や cloud のセッションへの送信はボードの名前に当たらないので出ない。
  mod 自身の送信 (メモの手渡し、`origin.kind: plugin`) は依存ではないので覚えない
- 最後の行: メモを 3 件まで (種類と title)。残りは `+n`
- ボードは `hooks/board-data.ts` で session.start、TaskUpdate の後、10 秒ごとに読み直す。B の pane を開いている間は
  B が 3 秒ごとに読み直し、band は同じ board を描く
- メモは同じボードの `here` の行の open のメモ (この repo のもの) を新しく置かれた順に並べる。TaskUpdate の結果が
  `completed` になったら、`park list --status open --task <task_id> --session <id> --cwd <cwd>` (そのタスクの途中で
  このセッションが置いたメモ) を先頭に寄せる。寄せるのは並びだけで、open でなくなったメモは次の読み直しで消え、
  辺もメモも無くなれば band ごと消える。どちらも `--session` で開いたセッションの domain のメモだけを引く
- band で選んだメモ (`target`) は B の pane と共有し、B でも同じものを開く
<!-- cc-premise: 2.1.303 hooks mods の ui.render AbovePrompt (hasSurvey、bodyColumns) と、TaskUpdate の結果の statusChange -->
<!-- cc-premise: 2.1.306 mods session.send は SendMessage tool と $.session.send の両方で起き、e.origin.kind と e.to を持つ -->
