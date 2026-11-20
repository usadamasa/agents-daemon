# 多重化ツールの backend

daemon は pane 操作を `multiplexer` の `Client` interface 越しに行い、実装は rex (`rex`。
呼び手が `backend` だけなので、その下に入れ子)。
`Client` は実装が満たす全部入りで、呼び手 (`monitor`、`daemon`、`doctor`、`verify`、`simulate`、`spawn`) は要る method だけを
自分の側で interface にして受ける。どの呼び手が何を使うかは `multiplexer` の package doc が持つ。
`backend` パッケージが起動時の環境に `REX_SESSION` があることを確かめ、無ければ `daemon --ensure` は
stderr に 1 行出して何もせずに戻る (素の端末の SessionStart で毎回 daemon を起こさない)。hook はこの判定を
持たず、バイナリの build は常に行う。同じバイナリを `statusline` / `ingest-statusline` が使い、
これらは多重化ツールを要らないため。backend は `status.json` の `backend` に書き、`status` が表示する。

画面の読み取りと送信の作法は [pane-io.md](pane-io.md)、Agent と AgentStatus の決め方は
[program-status.md](program-status.md) が持つ。

## rex の対応表

daemon は rex を control API (Unix socket の上の WebSocket) だけで操作し、rex の CLI は起こさない。
パッケージの分け方は `rex` の package doc が持つ。

| 操作 | rex の method |
| ---- | ---- |
| PaneList | daemon では購読の cache (下の「event の購読」)。購読していない呼び手と未同期の間は全列挙: `session.list`、session ごとに `session.list_blocks`、flavor が `com.superlogical.terminal.shell` の block ごとに `program_status` と `process` |
| PaneGet、PaneRead、PaneGeometry、SendText、SendKeys | `target.resolve` で block の session を引いてから、block の method (`com.superlogical.terminal.<名前>`) を打つ |
| PaneRead | `format` (`trim`、`unwrap`、色付きなら `format: vt`) の `content` に、改行で終わっていなければ改行を 1 つ足し、末尾 N 行に絞る。続きがあれば `blob_read` で読み足す |
| SendText | `write` (Enter は押さない) |
| SendKeys | key ごとに `block.event` の press と release (`enter` と `esc` だけを知る) |
| Reachable | `server.status` の `pid` が正 |
| session_id | `context/<sid>.json` の `multiplexer.block` から逆引き ([statusline-state.md](statusline-state.md)) |
| NewSession (`spawn` の Host。Client には無い) | `session.create` の `session_id` と `initial_windows[0]` の `window_id`・`block_ids[0]` (`multiplexer.Placement`)。block の command は `wrap.SessionCommand` (包みの sh。ARGV を起こす前に `spawned-session mark` で印を書き、ARGV が終わったら session の block が自分だけなら rex の CLI で `session kill` する)。argv は login shell の環境で 1 引数ずつ子へ渡る (空白、`$` を変えない。実測) |
| NewWindow (`spawn` の Host) | `target.resolve` (足し先を ID か label で引く) と `session.new_window` (`focus: false`)。resolve した session の ID と `window_id`・`block_ids[0]` を返す。command は `wrap.TabCommand` |
| SplitPane / ClosePane (`simulate` の使い捨て pane) | `session.new_split` (ratio 0.6、`shell: none`、`login: false`、`exit.on_completion: false`) と `block.close` |
| SessionBlocks / SessionKill (daemon の後片付け。Client には無く、daemon が型で確かめる) | `session.list_blocks` の block を flavor を問わず数える (`session_not_found` は session が無い)。`session.destroy` |

待機中のラベルは出さない。rex で表示に使える block の label は利用者が付けた label を潰すため。

接続は Client ごとに 1 本で、最初の呼び出しで張り、切れていれば次の呼び出しで張り直す。張ったら
`session.attach_all` (`event_scope: session`) を打つ (attach しない接続から session を名指すと `conflict`)。
request 1 回の制限時間は 10 秒。短命のサブコマンドは終わるときに `backend.Close` で close frame を送って閉じる。
作った block は `session.attach` の後に `session.disown_block` で手放す (作った接続が owner になり、owner の大きさで
terminal が描かれるのを避ける)。対象が無い失敗 (`not_found`、`session_not_found`、`target.resolve` の
`not_found`) は `api.IsNotFound` で拾い、`list_blocks` と block の method の間に閉じた block や session は読み飛ばして
PaneList の失敗に数えない。

全列挙は 1 回に `session.list` 1 回、session ごとに `list_blocks` 1 回、shell の block ごとに 2 回の request になる。
block が多い環境ほど時間が伸びるので、daemon は tick ごとには打たず、購読の cache から一覧を取る。

## event の購読

<!-- rex-premise: preview.30 api event_scope all の接続にだけ block_event が届く。payload は rex events --all --json の 1 行と同じ形 -->
<!-- rex-premise: preview.30 api process_changed は前景の入れ替わりの瞬間に push され、process の問い合わせでは起きない -->

daemon は起動時に `multiplexer.Watcher` の `Watch` で購読を 1 本張る (`rex/watch`)。doctor、inspect、simulate、spawn は
購読せず全列挙のまま。

1. 呼び出しと別の control 接続を張り、`session.attach_all` (`event_scope: all`) を打つ。この応答の後に起きた event が届く
2. 全列挙で cache を作る (呼び出しの接続で打つ)。全列挙の間に届いた event は捨てずに溜め、全列挙の後に順に当てる。
   購読は欠けず順序を保つので、snapshot より古い event が当たっても、その後の変化の event が後ろに必ずあり、
   cache は最新に追いつく。全列挙の後に購読の接続で `server.version` を 1 本打ち、その応答の seq までの event を
   当て終えてから公開する (応答は event を追い越しうるので、全列挙が読んだ変化の event が届く前に公開すると、
   その間だけ全列挙より古い状態が見える)
3. 以降は event を当て続け、`PaneList` は rex を叩かずに cache を `ToPane` へ渡す (`rex/blockstate`)

| event | cache への当て方 |
| ---- | ---- |
| `program_status_changed` | record を `id` で置き換える (root は空の id)。report は record を丸ごと置き換える |
| `program_status_removed` | `ids` の record を消す (`reason` は `child_exited` / `foreground` / `clear` など。理由を問わず消す) |
| `process_changed` | 前景を置き換える。`isForeground` の突き合わせはこの前景で行う |
| `child_exited` | 前景を空にする。`--keep-open` の block は終了後に `process_changed` が来ず、`idle` の record が残る |
| `block_created` | payload に flavor が無いので、その session に `list_blocks` を 1 回打ち、shell なら `program_status` と `process` で埋める |
| `session_created` | 新しい session の最初の block には `block_created` が来ないので、その session に `list_blocks` を 1 回打ち、shell の block を全部埋める |
| `block_closed` / `session_destroyed` | その block、その session の block を落とす |

それ以外の event (client の出入り、window、view の変化、ほかの block の event) は読み捨てる。

event の queue は 16384 件で、溢れたら close frame (1008) で接続を閉じて張り直す (読み手を待つと応答の受け取りが止まり、捨てると
欠けた event で cache を進める)。daemon が終わるとき (入れ替わりを含む) は購読の接続を close frame で閉じてから
終わる。購読が切れたら (接続の切断、queue の溢れ、壊れた event、`block_created` / `session_created` の後の
`list_blocks` の失敗) cache を捨てて未同期に戻し、1 秒から倍、上限 30 秒の間隔で張り直す。張り直した接続は seq を
数え直し、切れていた間の event は失われるので、張り直すたびに全列挙からやり直す。1 分以上続いた購読の後は間隔を
1 秒に戻す。未同期の間の `PaneList` は全列挙で、server が落ちていれば今までどおり tick の失敗に数える。
切れた理由と張り直しは daemon.log に 1 行ずつ出る。

## spawn の包みが叩く rex のバイナリ

<!-- rex-premise: preview.30 cli block の PATH は Versions/Current/… の 1 項だけ。Current は更新で中身が入れ替わり、前の build は次の更新で消える -->

spawn した pane の中で走る包み (`wrap`) だけが rex の CLI を叩く。包みに渡す rex のパスは、spawn のたびに
`wrap.Locate` が引く (`REX_BIN_PATH` があればそれ、無ければ PATH)。rex は block の PATH に
`…/RexServer/Versions/Current/…` の 1 項だけを載せる。自動更新は `Current/` の中身を入れ替え、前の数字名の
build を次の更新で消す。

<!-- rex-premise: preview.30 cli 既定の置き場は ~/Library/Application Support/com.superlogical.rex/RexServer/Versions/{Current,<build>}/Rex Server.app/Contents/MacOS/rex。ログインシェルの PATH には載らない -->

PATH で引けないときは、既定の置き場 `~/Library/Application Support/com.superlogical.rex/RexServer/Versions/` の
`Current`、無ければ一番新しい数字名の build の rex を引く。PATH に rex の項が無いプロセス (Finder や Dock から
起きた依存図のアプリと、その子の `park hand` や `spawn`) がこれに当たる。標準外の置き場は探さないので、そこに置くなら
`REX_BIN_PATH` で固定する。doctor の「rex の前提」の節は、包みが叩く rex のパスを出す。
