# 依存図

`agents-daemon deps` が出す依存図の JSON、それを描くアプリ (`gui/`) とセッションへの送達 (`tell`、`park hand`)、
model の `SendMessage` の結果に添える辺の書き方の通知 (mod)。パーキングのメモの見せ方と操作、park tool、
ファイルのメモの通知は [parking.md](parking.md) が持つ。

## mod の束ね方

`hooks/hooks.json` の `modules` に書ける module は 1 つなので、`hooks/register.ts` が
`hooks/park.tsx`、`hooks/notice.ts`、`hooks/handoff-notice.ts` の hook を束ね、皆が使う `session.start` を持つ。
依存図を描く mod (`/deps` の pane) は #364 でアプリに移して外した。mod に残るのは tool の結果に添える経路で、
セッションの中にしか置けないものだけ。

## 依存図の JSON (`deps`)

`agents-daemon deps` は stdin の `{}` を受けて `DepsData` を出す。JSON は `deps` パッケージが
`$STATE/tasks/` (PostToolUse hook の `ingest-task` が書く)、`$STATE/work/` (SessionStart / UserPromptSubmit / Stop /
SessionEnd hook の `ingest-work` が書く)、`status.json`、`$STATE/edges/`、`$STATE/parking/` から組む。
読み手 (アプリ、`hooks/handoff-notice.ts`) はどのセッションにも属さないので、「自分の repo」のような欄は無い。

- `graph` はタスクの依存の DAG (「依存の DAG」の節)
- `repos` は repo (`git rev-parse --git-common-dir` の main worktree) ごとの生きているセッション (`sessions`) と
  open のメモ (`memos`)。セッションは作業の cwd の repo に入り、タスクの無いセッションは作業の記録の cwd (無ければ pane の
  cwd) の repo に入る。メモだけの repo も載る。並びは見出し、鍵の順
- `sessions` の `subject` は、その repo で進行中のタスク (Key の順で最初のもの)、進行中の TodoWrite の項目、
  作業の記録の見せる題、pane の今の作業 (`agent_message`)、セッション名、cwd の末尾の順に選ぶ。並びは表示名、session ID の順

タスクの無いセッションは作業の記録 (`worklog`、#368) から `sid:<session_id>` のノードになる。model が Task 系ツールを
打つかどうかに関わらず、prompt を打ったセッションは全部載り、終了しても記録が消えるまで (7 日) `exited` で残る。題は
人が直した題 (`work set` の subject、origin human) > companion の推定の題 (mod `hooks/work-infer.ts` が `$.model.fork` で
会話から推定し inferred に書く) > 最初の prompt の 1 行目の順で、ノードの `origin` (prompt / inferred / human / pane) が
どれを見せているかを持つ (アプリは inferred に「会話から自動で付けた題 · 直す」の chip を添え、押すと題を直す欄が開く。
箱の副題には出どころを出さない)。summary は生きていれば pane の今の作業、終了して
いれば最後のターンの応答の 1 行目。記録の無い生きているセッション (plugin の更新前に起きたもの) は今まで通り pane の
名前から付ける (origin pane)。タスクのあるセッションには作業のノードを作らず、辺の端点の解き方は変えない。
<!-- cc-premise: 2.1.319 hooks UserPromptSubmit の prompt (peer の delivery は cross-session-message の包みごと)、Stop の last_assistant_message、SessionStart の source、SessionEnd の reason -->

**出どころの優先順と上書き**: 人の直した題 > 推定の題 > prompt の題は見せる順で、記録の上では subject (prompt か human) と
inferred が別の欄に残る。hook と companion は human の subject を書き換えず (`HumanSubjectKept`)、companion は subject では
なく inferred にだけ書く。推定はセッションの中で 2 回まで (失敗のやり直しを含む。`InferBounded`)。
- `recent_repos` はセッション台帳に載った repo (最後に見た時刻の新しい順)。新しいセッションを起こす repo の候補
- `stale_memo_days` は open のメモを「古い」とみなす日数 (`parking` の `StaleAfter`)

## 依存図のアプリ (`gui/`)

アプリは依存図をセッションの外の窓に箱と線で描き、タスクとメモの単体ビューに動詞を並べる操作盤。建て方と置き場は
README の「依存図のアプリ (agent-tasks)」。Go 側 (`gui/service.go`) はバイナリのサブコマンドを allow-list の中で
exec して stdout を返すだけで、JSON はフロントエンド (`gui/frontend/src/`) が読む。状態はバイナリが持ち、
数秒おきに `deps` を読み直す (窓が隠れている間は間隔を伸ばす)。操作のログの主体は固定の `app`。

- `graph.js` は DAG の索引、`ops.js` は操作の判定 (`candidates`、`canDismiss`、`canAdopt`、`canTell`、`isOwnTask`)、
  宛先の並べ方、タスクへ頼む文面の雛形。`actions.js` は操作 (結果の文面を返す)。`app.js` が描く
- バイナリだけで済む操作: 依存を足す・外す (`edge add` / `rm`)、メモを捨てる (`park set` / `park discard`)、
  終了したセッションのタスクを外す (`task dismiss`)・メモにする (`park adopt`)
- 送達を伴う操作: 生きているセッションのノードへ指示を送る、未完了のタスクを渡す (生きているセッションか新しく起こす
  repo)、着手・完了・削除・メモにする (持ち主のセッションに TaskUpdate を頼む)、同じセッションの blockedBy の
  解消を頼む。どれも `tell` で届け、タスクを渡すときは相手へ送ってから持ち主に消すよう頼む (新しく起こすときは
  `spawn` の後)。起きたか分からない (`foreground: false`) ときは消すよう頼まずに残す。メモにするときは handed の
  メモを open に戻してから頼み、頼めなくても open のまま残す (押し直すと依頼だけが送り直される)
- メモを渡す (タスクにする・issue にする): 宛先は生きているセッションと新しく起こす repo。`park hand` 1 回で
  取る・送る・戻すまで行う ([parking.md](parking.md))
- 送達が拒まれたとき (下の `tell` の判定) は開いている選択と打った文面を残し、理由を文面の下に出す
- 単体ビューの本文 (タスクの説明、今の作業、メモの detail、辺の note) の URL、issue / PR の参照、ファイルパスは `links.js` が
  リンクにし、Go の `Daemon.Open` が http / https の URL と在る絶対パスだけを `/usr/bin/open` へ渡す。ファイルは既定のアプリで
  開くので行番号は使わない。`#123` は `DepsRepo.github_repo` で補い、GitHub の repo でなければ素の文字のまま

### セッションへの送達 (`tell`)

`agents-daemon tell` (`tell` パッケージ、handoff の家族) は宛先の session ID の pane の入力欄に文面を打って Enter を
送る。利用者の操作は利用者の指示なので、peer メッセージではなく入力欄に打った文面としてそのターンの指示にする。

- 宛先の block は statusline の記録 (`context/<sid>.json`) を逆引きし、claude が前景のものを選ぶ (複数なら新しい block)
- 送る前に pane を読み、利用者の答えを待つ prompt (`multiplexer.WaitingOnPrompt`、permission / question / auth)、
  上限の画面 (`detect` の分類。cmd が注入する)、入力欄の打ちかけの文のどれかに当たれば送らずに exit 1 で理由を返す。
  入力欄は `❯` で始まる最後の行で、空のときは `❯` の後に NBSP だけ。`❯` の行が無ければ送らない。残りがあるときは
  制御シーケンス付き (`rex capture --format=vt`、`PaneReadOptions.Styled`) で読み直し、薄字 (SGR 2) だけなら Claude Code の
  次の prompt の提案とみなして送る (提案は打つと消える。実測)。素の字が 1 文字でもあれば打ちかけの文として拒む
- 文面の制御文字 (改行とタブ以外の C0、DEL、C1) は落としてから打つ。文面はメモ (セッションが書いたもの) や利用者の入力で、
  ESC が混じると pane ではキーとして読まれる (ターンの中断、埋め込まれた paste の終わりの印)
- 文面と Enter は別々に送る (monitor の再開と同じ作法)。改行かタブを含む文面は bracketed paste (`\x1b[200~` … `\x1b[201~`)
  で囲み、改行が Enter に、タブがキーにならないようにする (実測: 3 行が 1 つの prompt として入る)
- monitor と違って Escape は送らない。Escape は `/rate-limit-options` のメニューを閉じるためのもので、ターンの途中の
  セッションに送ると中断になる。ターンの途中の pane には送り、文面は入力欄に溜まって次のターンになる
- 文面が上限の検知語を含むときは送ったうえで `warning` を返す (入力欄に残る間は daemon が上限と取り違えうる。#257)
- 結果は `tell` の出来事 (to、pane、delivered、reason) に書く
<!-- cc-premise: 2.1.319 input-line 入力欄は ❯ で始まる行で、空のときは NBSP。複数行は bracketed paste で 1 つの貼り付けになる。次の prompt の提案は薄字で打つと消える -->

アプリは rex の外で起きるので、`spawn` / `tell` / `park hand` は REX_SESSION を要求しない (`backend.ConnectRex`)。
`spawn` は起こすたびに `rex session new` で専用の rex session を作る。起きた claude は rex の案内に従って
自分の REX_SESSION を rename するので、起こす側の session に相乗りさせない (#376)。

## 依存の DAG (`graph`)

`graph` はセッションをまたぐタスクの依存の DAG (#134)。ノードはタスク 1 つ (`task:<sid>/<tid>`)、TodoWrite の
項目 1 つ (`todo:<sid>/<n>`)、タスクの無い生きているセッション 1 つ (`sid:<sid>`) で、辺は同じセッションの
`blockedBy` (`kind: task`) と `$STATE/edges/` の辺 (`kind: edge`) を 1 つにしたもの。ノードの `key` は Ref
(`ref`) そのもので、辺の端点と同じ形になる。読み手は key を切り出さず、端点にできるかは `endpoint`
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
`ledger`) を書いてから結びつける (`edge` の `Bind`)。台帳の名前は ListAgents の名前で、daemon が Claude Code の
セッションの登録簿 (`$CLAUDE_CONFIG_DIR/sessions/<pid>.json` の `name`、`claudesessions`) から引く。statusline の
`session_name` は会話の題で、名前を付けずに起動したセッションでは ListAgents の名前と一致しない。依存図のノードの
`session_name` も台帳のこの名前で、表示名 (`session`) と題は statusline の `session_name` から付ける。
<!-- cc-premise: 2.1.316 peer-messaging sessions/<pid>.json の name が ListAgents の名前 -->
候補は名前が一致し、辺の
created_at に生きていたか後から現れた (last_seen が created_at 以後の) セッション (`ledger.Pick`)。辺を書いた時点で
動いていた候補 (first_seen が created_at 以前) があれば first_seen の最も新しいもの、無ければ created_at より後に
最初に現れたものを選ぶ。渡す側は `claude --name` で起こしてすぐ `edge add` を書くので、直前に起きた同名のセッションが
相手になる。古い同名のセッションを待たせたかった場合のような取り違えは、`edge rm` してから `sid:` で書き直す。結びつけた端点は `sid:<sid>` に書き換え、元の参照を `bound_from` に残し、二度と結び直さない。
tick ごとにするのは、渡した側が `edge add` を書いてから数秒で端点がそのセッションのノードにつながり、アプリが置き場の
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

止まっているかもバイナリが決めて渡し、読み手は状態の語から決めない。`halted` はセッション自身が止まっている
(`question` / `permission` / `limit` / `error`) ノードで、原因の鎖で先に選ぶ上流に使う。`stuck` は
`halted` か、完了していない待ちの輪のノードで、footer の数、箱の下流の数、longest chain の起点に使う。

段 (`depth`)・下流の数・待ちの輪・原因の鎖・最長の鎖の数え方と、`rows` / `rows_with_completed` (位相順の行とレーンの
割り当て) は `graph/dag` が持つ。アプリは `depth` を dagre の段にし、`rows` は読まない。

## 渡した宛先の辺の確かめ

`hooks/handoff-notice.ts` は main の model の `SendMessage` (文面のあるもの) が成功したら、
`deps` (`hooks/deps-data.ts` の `loadDeps`) を読み、宛先 (末尾の ` [ref]` は落とす) が
依存図に生きていて済んでいないノードを持つセッションで、`kind: edge` の辺が端点 (そのセッションのタスクへ解決された
端点か、記録の `session:<名前>` の参照) に 1 本も無ければ (`hooks/graph.ts` の `unlinked`)、tool の結果の `context` に
辺の書き方を添える (#172)。文面は `edge add` へ渡す `EdgeAddRequest` を両方の向きで示す。宛先が待つ向きは
after を `session:<名前>` と宛先の cwd にし、自分のタスクが宛先を待つ向きは before をその端点、after を `task` 欄
(自分のタスクの id だけ。参照と cwd はバイナリが埋める、#293) にする。`agents-daemon:peer-sessions` skill の
「依存の辺を書く」を指し、返事や報告なら辺は要らないと添える。

- 辺を書くのは渡した側の model だけなので、人ではなく model に伝える
- subagent や teammate、cloud のセッションへの送信は依存図の名前に当たらないので添えない
- アプリの送達 (`tell`、`park hand`) は `tool.call` を通らないので見ない
- 送信のエラー、subagent の送信、文面の無い送信 (`notify_when_idle` だけ、shutdown などの要求)、依存図の読み込みの
  失敗では何も添えない
<!-- cc-premise: 2.1.310 mods SendMessage の tool.call は e.to と e.message を持ち、context は model だけが読む -->
