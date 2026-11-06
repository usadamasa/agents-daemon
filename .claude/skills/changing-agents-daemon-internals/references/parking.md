# パーキングロット

パーキングのメモの記録と手渡し (`park hand`)、アプリでの見せ方と操作、model が呼ぶ park tool、model の tool の結果に
添えるファイルのメモの通知 (mod)。メモの一覧を描くアプリと mod の束ね方は [deps.md](deps.md) が持つ。

## パーキングのメモ

パーキングロットに置くもの 1 つを、docs と CLI の文言では「メモ」と呼ぶ。コードでは `parking.Item` で、
`park add` の結果の `item` と `park list` の配列の要素がこれに当たる。識別子と JSON のキーは `item` のまま。

`agents-daemon deps` の JSON は repo ごとに open のメモ (`$STATE/parking/`) を `memos` に持ち、メモだけの repo も
repo にする。

メモは種類 (`kind`: aside / scope / preference) と出自 (`origin`) を別に持つ。`placed` は model が park tool で
置いたメモ、`adopted` は終了したセッションの未完了のタスクを `park adopt` で移したメモ。adopted のメモの種類は
`scope` にする (残ったタスクを拾うかは範囲の相談で、既定 (`default_taken`) は「未完了のまま」)。同じメモか
(repo と正規化した title) は出自を見ずに決め、まとめたメモの出自は最初のメモのまま。adopted のメモの種類には
`・adopted` を添えて出す (アプリの `memo.js`、`parkhand` の文面、`hooks/notice.ts` のファイルのメモの通知が同じ語を持つ)。

handed のメモは宛先を `handed_to` に持つ。peer と自セッションは `sid:<session_id>`、新しく起こすセッションは
起こす先の `repo:<path>`。起こしたセッションの ID は渡した後に分かるが、宛先は repo のまま書き直さない (取ってから
書き直すまでの間に宛先の違うメモが見え、書き直しが失敗したときの扱いも増えるため)。

### メモとタスクの照合

メモの側にはタスクを書き戻さない (#213)。依存図を組むたびに、タスクの記録の description から
メモの id (`parking` の `Mentioned`、`newID` と同じ形) を拾い、status を問わずメモと照合して、
そのタスクのノードの `memos` (id、title、status。description に書かれた順) に付ける。アプリは選んだタスクの単体ビューに
「扱うメモ」として出す。handed のメモは repo の `memos` から消え、渡した先のタスクの詳細に現れる。
handed / discarded のメモが保持期間 (7 日) で消えた後は、description に id が残っていても照合できず、出さない。

### メモの見せ方と操作

メモの見せ方と操作はアプリ (`gui/frontend/src/`、`memo.js` と `actions.js`) が持つ。メモの行は最後に置かれた時刻 (JST)
を末尾に出し、どのメモが古いかを列で見比べられるようにする。操作はどれも `agents-daemon park ...` に操作主体 (`app`) の
`session_id` を添えた JSON を stdin で渡す (ログに残すだけで、メモの絞り込みには使わない)。入力の形は `protocol` の
schema の `Park*Request` が持つ。

- 捨てる: `park set` を status discarded で
- 古いメモをまとめて捨てる (「n 日より古いメモ m 件を捨てる」): 最後に置かれてから 30 日より経った open のメモがあるときだけ
  パーキングの見出しに出し、`park discard` (days 30) で open のメモをまとめて discarded にする。30 日はバイナリ
  (`parking` の `StaleAfter`) が持ち、`deps` の `stale_memo_days` でアプリに渡す。CLI に既定は無い。housekeeping が
  open を 90 日で黙って消す前に、人が見て捨てる区切り (関係は `daemon/retention_test.go`)。捨てたメモは 7 日残り、`logs --events` の `park.discard` に残る id を `park set` で status open に戻せる
- タスクにする・issue にする: 宛先は生きているセッションか、新しく起こすセッションの repo。タスクにするの宛先はメモの
  repo の `sessions`、issue にするの宛先はメモの repo の `sessions` を先に、続けてほかの repo の `sessions`。宛先の名前には
  `subject` を添える (#191)。補足欄に書いた文は渡す文面の末尾に足す (#310)
- 新しいセッションを起こして渡す: 生きているセッションの無い repo へ渡す経路 (#162)。宛先の下に repo の候補 (メモの
  repo、依存図の repo、セッション台帳で最近動いた repo の新しい順。#309) を並べる。台帳の repo は deps の `recent_repos`
  が渡し、最後に見た時刻を「n 日前」で添える。先頭の 8 件 (`SPAWN_SHOWN`) を超えた分は「ほか n 件…」で出す。候補に無い
  repo は macOS のフォルダ選択で指定する
- パーキングへ移す (「メモにする」): 終了したセッションの未完了のタスクのノードを `park adopt` (ref にノードの key `task:<sid>/<tid>`) で
  出自 `adopted` (種類 `scope`) のメモにし、タスクの記録を消す。model の tool は出自を書けない。アプリは daemon が
  status.json を書いているとき (`updated_at` があるとき) だけこの操作を出し、バイナリは動いている daemon がタスクの
  記録より後に書いた status.json に pane が無いことを確かめてから移す (`daemonstate` の `SessionExited`)。
  daemon が止まっていると生きているセッションも exited と描かれるため

### メモの手渡し (`park hand`)

タスクにする・issue にするは `agents-daemon park hand` (`parkhand` パッケージ、handoff の家族) 1 回で行う。
もとは mod が `park set`、送信、`park set` の 3 回のコマンドで行っていた手順を、1 つのプロセスが直列に行う。

- 取る: `park set` と同じ書き換え (status handed、from open、宛先 to_session か to_repo)。今の status が open でなければ
  失敗して何もせず exit 1 (ほかが先に渡した、捨てた)。2 つの呼び手が同じメモを渡そうとしても取れるのは 1 つ。
  宛先は status と同じ書き込みで書く (別の書き込みにすると宛先の無い handed が見える)
- 文面を組む: タスクにする (TaskCreate を頼み、description にメモの id を入れさせる。着手の時期は受け手の計画に任せる) か
  issue にする (起票して URL を返させる)。どちらも先にメモの前提が今のコードと計画で成り立つかを確かめさせ、
  成り立たなければ理由を 1 行で返して終えさせる (`parkhand` の `premiseCheck`)。メモは置いた時点の前提で書かれ、渡すまでに
  レビューや別の変更で前提が消えることがある。受け手はメモの status を触らない。渡したメモは handed で一覧に
  出ず、タスクが無ければどこにも出ないまま 7 日で消える。補足を書いたセッションの名前は statusline の記録から引く
- 送る: 生きているセッションへは `tell` で入力欄に打つ ([deps.md](deps.md))。新しく起こすセッションへは `spawn` で
  `claude --session-id <uuid> --model $CLAUDE_CODE_SUBAGENT_MODEL -- <本文>` を repo を cwd にした rex の window に起こし
  (フォーカスは奪わず、終了後も pane を残す)、claude が前景になってから `SpawnResult` を返す。本文は起こした経緯の
  1 行に続けて同じ文面で、issue はメモの repo ではなく起こした repo を起票先にする。モデルを subagent 用に揃えるのは
  peer-sessions skill と同じ
- 戻す: `tell` が送らなかった、`spawn` が window を作れなかったときは status open、from handed で戻し、宛先も空になる。
  待ち切れたとき (`foreground: false`) は claude が起きているかもしれないので戻さず handed のまま残す (戻すと押し直しで
  2 つ目が起きる)。Discard も from open で、取られたメモは捨てない
- 結果は `ParkHandResult` (delivered、status、to、pane_id、reason、warning) で、取れた後は届かなくても exit 0。
  出来事のログには `park.set`、`tell`、`park.hand` の順で残る

手順の交錯で破れない性質は `models/handoff` が持ち (3 歩はそれぞれ別のファイルの書き込みと送信なので、2 つの呼び手の
手順は交錯しうる)、実装に対しては `parking` と `tasklog` の `invariants_test.go` が実物の Store の交錯で確かめる。
<!-- cc-premise: 2.1.306 cli claude [prompt] は最初のターンの本文、--session-id <uuid> でセッション ID を固定できる -->

### 時刻の書き方

人が読む表示 (アプリのメモの行と置いた経緯) は、メモの時刻を JST の `MM/DD HH:MM` で出す。変換は
`gui/frontend/src/memo.js` の `stamp` が 1 箇所で持ち、記録の offset にもアプリの時刻帯にも頼らない。

メモの id (`20261018T162844-53de0c9a`) の時刻は UTC にする。id はファイル名と `park set <id>` の鍵なので、端末の
時刻帯に左右されず、名前の並びが置いた順と一致する形にする。id の時刻は人に見せず、表示には `sources[].at` を使う。

記録に書く時刻 (`sources[].at`、タスクの記録、`status.json`、ログ) は offset 付きの RFC 3339 のままにし、
offset を揃えない。どの offset で書いても同じ瞬間に戻せるので、揃えるのは表示の側だけで足りる。

## park tool

`hooks/park.tsx` は model が呼べる tool `mcp__agents-daemon__park` を登録する (パーキングロットの置く側)。
呼ばれたら `agents-daemon park add` に、model が書いた欄 (memo) とこのセッションの `session_id` と cwd を JSON で
stdin に渡す。inputSchema は schema の `Memo` そのもので、`session_id` と cwd は model が書けない。結果の `created` で「メモを置きました」と
「既に置かれていたメモにまとめました」を分けて返す。
アプリは開いている間だけ自分で読み直すので、置いたメモは次の読み直しで一覧に出る。

同じ mod は tool `mcp__agents-daemon__unpark` も登録する。置いたメモの前提が自分の作業の中で消えたとき (該当箇所を
消した、別の変更で直した) に、model が id を渡して取り下げる。mod は id とこのセッションの `session_id` を
`park withdraw` に渡し、バイナリ (`parking` の `Withdraw`) は sources のセッションがすべて呼んだセッションで、status が
open のときだけ discarded にする。ほかのセッションも置いたメモはそのセッションの前提が残りうるので、理由とアプリの
「捨てる」を返す。handed のメモはアプリの一覧に出ないので、作ったタスクを TaskUpdate で閉じるよう返す。open だけを書き換えるのでアプリの「捨てる」と同じ遷移で、handed から discarded への遷移は無い
(`models/handoff` の `HandedWhenDelivered`)。
<!-- cc-premise: 2.1.302 hooks mods の tool.register / tool.call -->
<!-- cc-premise: 2.1.302 hooks modules は 1 つ、matcher の無い hook は event ごとに 1 つ -->
<!-- cc-premise: 2.1.302 hooks tool.register は local の port を開く (sandbox の中の claude -p では落ちる) -->

## ファイルのメモの通知

`hooks/notice.ts` は main の model の Read / Edit / Write が成功したら、
`park list` でこの repo の open のメモ (status open、here) を読み、触ったファイルを
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
