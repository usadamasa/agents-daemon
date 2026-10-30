# 多重化ツールの backend

daemon は pane 操作を `multiplexer` の `Client` interface 越しに行い、実装は rex (`rexcli`。
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

| 操作 | rex |
| ---- | ---- |
| PaneList | daemon では `events --all --json` の購読の cache (下の「events の購読」)。購読していない呼び手と未同期の間は全列挙: `ls --json`、session ごとに `block ls -s S --json`、flavor が `com.superlogical.terminal.shell` の block ごとに `block call -b B program_status` と `block call -b B process` |
| PaneRead | `capture -b B --trim --unwrap` の末尾 N 行 |
| SendText | `send -b B TEXT` |
| SendKeys | `send-key -b B KEY...` |
| Reachable | `api call server.status` の `pid` が正 |
| session_id | `context/<sid>.json` の `multiplexer.block` から逆引き ([statusline-state.md](statusline-state.md)) |
| RunWindow (`spawn` の Host。Client には無い) | `run --focus=false --keep-open -s S --cwd DIR --label L --json -- ARGV...` の `block_ids[0]`。`-s` は childEnv が `REX_SESSION` を外すので明示する。argv は shell を介さず 1 引数ずつ子へ渡る (空白、引用符、改行、`$` を変えない。実測) |

待機中のラベルは出さない。rex で表示に使える `block rename` は利用者が付けた label を潰すため。

rex の呼び出しには全部 `--autostart=false` を付け、子プロセスの環境から `REX_SESSION` / `REX_BLOCK` を外す。
`REX_SESSION` があると rex は block の解決をその session の中に閉じ、外せば完全 ID で別 session の block も
解決する (実測)。`block ls` と `block call` の間に閉じた block (exit 3) は読み飛ばし、PaneList の失敗に数えない。

全列挙は 1 回に `ls` 1 回、session ごとに `block ls` 1 回、shell の block ごとに 2 回の fork になる。
block が多い環境ほど時間が伸びるので、daemon は tick ごとには打たず、購読の cache から一覧を取る。

## events の購読

<!-- rex-premise: preview.22 cli events --all --json は全 session の block_event を 1 行 1 event で server が切れるまで流す。plain の events には block_event が来ない -->
<!-- rex-premise: preview.22 cli process_changed は前景の入れ替わりの瞬間に push され、block call process の問い合わせでは起きない -->

daemon は起動時に `multiplexer.Watcher` の `Watch` で `rex --autostart=false events --all --json` を 1 本張る
(`rexcli/watch.go`)。doctor、inspect、simulate、spawn は購読せず全列挙のまま。

1. 最初の行を受けて購読が始まったことを確かめる。rex の CLI の呼び出しはそれだけで `client_joined` を流すので、
   2 秒待って来なければ `api call server.status` を 1 回打って起こす
2. 全列挙で cache を作る。全列挙の間に届いた event は捨てずに溜め、全列挙の後に順に当てる。stream は欠けず順序を
   保つので、snapshot より古い event が当たっても、その後の変化の event が後ろに必ずあり、cache は最新に追いつく
3. 以降は event を当て続け、`PaneList` は rex を叩かずに cache を `ToPane` へ渡す (`rexcli/blockstate`)

| event | cache への当て方 |
| ---- | ---- |
| `program_status_changed` | record を `id` で置き換える (root は空の id)。report は record を丸ごと置き換える |
| `program_status_removed` | `ids` の record を消す (`reason` は `child_exited` / `foreground` / `clear` など。理由を問わず消す) |
| `process_changed` | 前景を置き換える。`isForeground` の突き合わせはこの前景で行う |
| `child_exited` | 前景を空にする。`--keep-open` の block は終了後に `process_changed` が来ず、`idle` の record が残る |
| `block_created` | payload に flavor が無いので、その session に `block ls` を 1 回打ち、shell なら `program_status` と `process` で埋める |
| `session_created` | 新しい session の最初の block には `block_created` が来ないので、その session に `block ls` を 1 回打ち、shell の block を全部埋める |
| `block_closed` / `session_destroyed` | その block、その session の block を落とす |

`client_joined` / `client_left` と session ごとの `client_connected` / `client_disconnected` は rex の CLI の呼び出しの
たびに流れる雑音で、読む側の queue に入れる前に落とす。

daemon が終わるとき (入れ替わりを含む) は購読を止め、events の子を刈り取ってから終わる (上限 2 秒)。
stream が切れたら (EOF、壊れた行、`block_created` / `session_created` の後の `block ls` の失敗) cache を捨てて未同期に戻し、1 秒から倍、
上限 30 秒の間隔で張り直す。1 分以上続いた購読の後は間隔を 1 秒に戻す。未同期の間の `PaneList` は全列挙で、
server が落ちていれば今までどおり tick の失敗に数える。切れた理由と張り直しは daemon.log に 1 行ずつ出る。

## rex のバイナリの引き直し

<!-- rex-premise: preview.22 cli block の PATH は Versions/Current/… と Versions/<build>/… の順。Current は更新で中身が入れ替わり、前の build は次の更新で消える -->

rex のバイナリは最初の呼び出しで PATH から絶対パスに引いて覚える (`REX_BIN_PATH` があればそれで固定)。
rex は block の PATH に `…/RexServer/Versions/Current/…` を先に、`…/RexServer/Versions/<build>/…` をその後ろに
載せる。自動更新は `Current/` の中身を入れ替え、前の数字名の build を次の更新で消す。最初の解決は先頭の `Current`
の項に当たり、その項は更新をまたいで同じパスのまま残るので、覚えたパスは普段そのまま使える。
呼び出しで rex のバイナリが見つからなければ、PATH の `Versions/` から一番新しい数字名の build の rex を引き直し、
同じ呼び出しを 1 回やり直す。数字名の項だけを持つ PATH でその build が消えたときに起きる。このやり直しは tick の
失敗に数えない。
`REX_BIN_PATH` で固定しているときは引き直さない (`rexcli/locate.go`)。
doctor の「rex CLI の前提」の節は、解決した rex のパスを版と並べて出す。
