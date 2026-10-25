# 多重化ツールの backend

daemon は pane 操作を `internal/multiplexer` の `Client` interface 越しに行い、実装は rex (`internal/rexcli`)。
`internal/backend` が起動時の環境に `REX_SESSION` があることを確かめ、無ければ `daemon --ensure` は
stderr に 1 行出して何もせずに戻る (素の端末の SessionStart で毎回 daemon を起こさない)。hook はこの判定を
持たず、バイナリの build は常に行う。同じバイナリを `statusline` / `ingest-statusline` が使い、
これらは多重化ツールを要らないため。backend は `status.json` の `backend` に書き、`status` が表示する。

画面の読み取りと送信の作法は [pane-io.md](pane-io.md)、Agent と AgentStatus の決め方は
[program-status.md](program-status.md) が持つ。

## rex の対応表

| 操作 | rex |
| ---- | ---- |
| PaneList | `ls --json`、session ごとに `block ls -s S --json`、flavor が `com.superlogical.terminal.shell` の block ごとに `block call -b B program_status` と `block call -b B process` |
| PaneRead | `capture -b B --trim --unwrap` の末尾 N 行 |
| SendText | `send -b B TEXT` |
| SendKeys | `send-key -b B KEY...` |
| Reachable | `api call server.status` の `pid` が正 |
| session_id | `context/<sid>.json` の `multiplexer.block` から逆引き ([statusline-state.md](statusline-state.md)) |
| RunWindow (`internal/spawn` の Host。Client には無い) | `run --focus=false --keep-open -s S --cwd DIR --label L --json -- ARGV...` の `block_ids[0]`。`-s` は childEnv が `REX_SESSION` を外すので明示する。argv は shell を介さず 1 引数ずつ子へ渡る (空白、引用符、改行、`$` を変えない。実測) |

待機中のラベルは出さない。rex で表示に使える `block rename` は利用者が付けた label を潰すため。

rex の呼び出しには全部 `--autostart=false` を付け、子プロセスの環境から `REX_SESSION` / `REX_BLOCK` を外す。
`REX_SESSION` があると rex は block の解決をその session の中に閉じ、外せば完全 ID で別 session の block も
解決する (実測)。`block ls` と `block call` の間に閉じた block (exit 3) は読み飛ばし、PaneList の失敗に数えない。

rex の列挙は 1 tick に `ls` 1 回、session ごとに `block ls` 1 回、shell の block ごとに 2 回の fork になる。
block が多い環境ほど tick あたりの時間が伸びる。

## rex のバイナリの引き直し

<!-- rex-premise: preview.20 cli block の PATH は Versions/Current/… と Versions/<build>/… の順。Current は更新で中身が入れ替わり、前の build は次の更新で消える -->

rex のバイナリは最初の呼び出しで PATH から絶対パスに引いて覚える (`REX_BIN_PATH` があればそれで固定)。
rex は block の PATH に `…/RexServer/Versions/Current/…` を先に、`…/RexServer/Versions/<build>/…` をその後ろに
載せる。自動更新は `Current/` の中身を入れ替え、前の数字名の build を次の更新で消す。最初の解決は先頭の `Current`
の項に当たり、その項は更新をまたいで同じパスのまま残るので、覚えたパスは普段そのまま使える。
呼び出しで rex のバイナリが見つからなければ、PATH の `Versions/` から一番新しい数字名の build の rex を引き直し、
同じ呼び出しを 1 回やり直す。数字名の項だけを持つ PATH でその build が消えたときに起きる。このやり直しは tick の
失敗に数えない。
`REX_BIN_PATH` で固定しているときは引き直さない (`internal/rexcli/locate.go`)。
doctor の「rex CLI の前提」の節は、解決した rex のパスを版と並べて出す。
