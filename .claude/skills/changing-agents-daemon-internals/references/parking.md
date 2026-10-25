# パーキングロット (mod)

パーキングのメモの見せ方と操作、model が呼ぶ park tool、model の tool の結果に添えるファイルのメモの通知。
メモの節を描く pane と mod の束ね方は [deps.md](deps.md) が持つ。

## パーキングのメモ

パーキングロットに置くもの 1 つを、docs と CLI の文言では「メモ」と呼ぶ。コードでは `parking.Item` で、
`park add` の結果の `item` と `park list` の配列の要素がこれに当たる。識別子と JSON のキーは `item` のまま。

`agents-daemon deps` の JSON は repo ごとに open のメモ (`$STATE/parking/`) を `memos` に持ち、メモだけの repo も
repo にする。`here` は開いたセッションの repo の鍵で、pane はこの repo を自セッションの repo として扱う。

### メモとタスクの照合

メモの側にはタスクを書き戻さない (#213)。依存図を組むたびに、タスクの記録の description から
メモの id (`internal/parking` の `Mentioned`、`newID` と同じ形) を拾い、status を問わずメモと照合して、
そのタスクのノードの `memos` (id、title、status。description に書かれた順) に付ける。mod は選んだタスクの詳細に
`◇ <title> (<status>)` で出す。handed のメモは repo の `memos` から消え、渡した先のタスクの詳細に現れる。
handed / discarded のメモが保持期間 (7 日) で消えた後は、description に id が残っていても照合できず、出さない。

### メモの見せ方と操作

メモの見せ方と操作は `hooks/parking.ts` が持ち、DAG の描き方には依存しない。メモの行は最後に置かれた時刻 (JST) を
先頭に出し、どのメモが古いかを列で見比べられるようにする。操作はどれも
`agents-daemon park ... --session <id> --cwd <cwd>` を呼ぶ (session はログに残すだけで、メモの絞り込みには使わない)。

- 捨てる: `park set <id> --status discarded`
- 古いメモをまとめて捨てる (`Discard N older than 30d`): 最後に置かれてから 30 日より経った open のメモがあるときだけ
  パーキングの見出しに出し、`park discard --days 30` で open のメモをまとめて discarded にする。30 日は mod
  (`hooks/deps.tsx` の `STALE_DAYS`) が持ち、CLI に既定は無い。housekeeping が open を 90 日で黙って消す前に、人が見て
  捨てる区切り。捨てたメモは 7 日残り、`logs --events` の `park.discard` に残る id を `park set <id> --status open` で戻せる
- タスクにする: メモの repo が開いたセッションの repo (`here`) なら自セッション (`このセッション`) を先に、続けて同じ repo の
  `sessions` へ TaskCreate を頼む文面を送る。文面は着手の時期を指示せず受け手の計画 (タスクの依存) に
  任せ、タスクの description にメモの id を入れるよう頼む (既にこのメモを扱うタスクがあれば書き足すだけ)。
  照合の規則は「メモとタスクの照合」
- issue にするよう頼む: 自セッションを先に、メモの repo の `sessions`、ほかの repo の `sessions` の順に頼む
- 宛先の名前には `sessions` の `subject` を添える (#191)
- 自セッションは生きているのが自明なので、`sessions` や daemon の有無にかかわらず宛先に出す。自分へは peer メッセージを
  送れないので `$.prompt.submit` で自分の入力へ渡し、idle になってから 1 ターンとして入る (#190)
- 新しいセッションを起こして渡す (`To task → new session…`、`Ask for issue → new session…`): 生きているセッションの
  無い repo へ渡す経路 (#162)。押すと repo の候補 (メモの repo、開いたセッションの repo、ほかの repo) と
  絶対パスの入力欄を出し、選んだ repo で `agents-daemon spawn --repo <path>` に本文を stdin で
  渡す。バイナリが `claude --session-id <uuid> --model $CLAUDE_CODE_SUBAGENT_MODEL -- <本文>` を repo を cwd にした
  rex の window に起こし (`internal/spawn`、フォーカスは奪わず、終了後も pane を残す)、claude が前景になってから
  `{session_id, pane_id, foreground}` を返す。待ち切れたときは `foreground: false` と `detail` で返し、mod はメモを
  handed のまま残して pane を知らせる (起きたか分からないメモを戻すと、押し直しで 2 つ目が起きる)。本文は最初の
  ターンとして入るので、peer メッセージの inbox を待たない。文面は起こした経緯の 1 行に続けて peer へ送るものと同じで、
  issue はメモの repo ではなく起こした repo を起票先にする。モデルを subagent 用に揃えるのは peer-sessions skill と同じ
- タスクにする・issue にするよう頼むは、送る前に `park set <id> --status handed --from open` でメモを取る (今の status が
  open でなければ失敗し、送らない。2 つのセッションが同じメモを渡そうとしても取れるのは 1 つ)。`$.session.send` が
  届かなかった、`$.prompt.submit` が drop された、`spawn` が window を作れなかったときは `--status open --from handed` で
  戻す。Discard も `--from open` で、取られたメモは捨てない。手順の交錯で破れない性質は `models/handoff` が持ち、
  実装に対しては `internal/parking` と `internal/tasklog` の `invariants_test.go` が実物の Store の交錯で確かめる
<!-- cc-premise: 2.1.306 mods $.prompt.submit は idle になってから自分のターンとして入り、drop で入らなかったことが分かる -->
<!-- cc-premise: 2.1.306 cli claude [prompt] は最初のターンの本文、--session-id <uuid> でセッション ID を固定できる -->
- パーキングへ移す (`To parking`): 終了したセッションの未完了のタスクのノードを `park adopt <ノードの key>` (`task:<sid>/<tid>`) で
  `kind: task` のメモにし、タスクの記録を消す。`kind: task` は model の tool からは置けない。pane は daemon が
  status.json を書いているとき (`updated_at` があるとき) だけこの操作を出し、バイナリは動いている daemon がタスクの
  記録より後に書いた status.json に pane が無いことを確かめてから移す (`internal/verify` の `SessionExited`)。
  daemon が止まっていると生きているセッションも exited と描かれるため

### 時刻の書き方

人が読む表示 (pane のメモの行と詳細) は、メモの時刻を JST の `MM/DD HH:MM` で出す。変換は
`hooks/parking.ts` の `stamp` が 1 箇所で持ち、記録の offset にも mod の時刻帯にも頼らない。

メモの id (`20261018T162844-53de0c9a`) の時刻は UTC にする。id はファイル名と `park set <id>` の鍵なので、端末の
時刻帯に左右されず、名前の並びが置いた順と一致する形にする。id の時刻は人に見せず、表示には `sources[].at` を使う。

記録に書く時刻 (`sources[].at`、タスクの記録、`status.json`、ログ) は offset 付きの RFC 3339 のままにし、
offset を揃えない。どの offset で書いても同じ瞬間に戻せるので、揃えるのは表示の側だけで足りる。

## park tool

`hooks/park.tsx` は model が呼べる tool `mcp__agents-daemon__park` を登録する (パーキングロットの置く側)。
呼ばれたら `agents-daemon park add --cwd <cwd>` に、model が書いた欄とこのセッションの `session_id` を JSON で
stdin に渡す。`session_id` と cwd は inputSchema に無く、model は書けない。結果の `created` で「メモを置きました」と
「既に置かれていたメモにまとめました」を分けて返す。
依存図の pane は開いている間だけ自分で読み直すので、置いたメモは次の読み直しで pane に出る。
<!-- cc-premise: 2.1.302 hooks mods の tool.register / tool.call -->
<!-- cc-premise: 2.1.302 hooks modules は 1 つ、matcher の無い hook は event ごとに 1 つ -->
<!-- cc-premise: 2.1.302 hooks tool.register は local の port を開く (sandbox の中の claude -p では落ちる) -->

## ファイルのメモの通知

`hooks/notice.ts` は main の model の Read / Edit / Write が成功したら、
`park list --status open --here --session <id> --cwd <cwd>` (この repo の open のメモ) を読み、触ったファイルを
`files` に持つメモのうち、ほかのセッションも置いたものを tool の結果の `context` に添える。人には出さない。

- パスはメモの `repo` からの相対に揃えて比べる。repo の下の worktree (`.claude/worktrees/<name>/`) の中のパスは
  worktree からの相対にする
- 伝えたメモの id は `told` の atom に置き、同じ context で 2 度は伝えない。一覧は `update` の前に読み、選ぶのと
  `told` に足すのは 1 回の `update` の中で行う (並列の tool の hook が同じメモを 2 回添えない)
- main の会話が compact されたら (`session.compact` の結果が skip でなく、`precompute` でも subagent の compact でもない)
  `told` を空にし、compact の後に触ったファイルのメモを伝え直す (#273)
- subagent の tool (`e.agentId` がある)、エラーや deny の結果、`park list` の失敗では何も添えない
- 手順の交錯で破れない性質 (`AtMostOnce`、`NoneMissed`、`Informed`) は `models/handoff/FileNotice.tla` が持つ
<!-- cc-premise: 2.1.310 mods tool.call の結果の context は model だけが読み、e.agentId は subagent にだけ付く -->
<!-- cc-premise: 2.1.310 mods session.compact の next(e) は messages か skip、precompute は会話を置き換えない -->
