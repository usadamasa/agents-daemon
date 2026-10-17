# rex の到達性と block の対応

setup skill の 2 段目を rex で行うときの手順。

## 到達性

```sh
rex --autostart=false api call server.status | jq -c '{pid, session_count}'
```

失敗するか `pid` が無ければ NG。rex の server を起こしてもらう。daemon の到達確認 (`Reachable`) も同じ呼び出しを見る。

## sandbox が socket を塞いでいるとき

`connect: operation not permitted` なら sandbox が止めている。`sandbox.network.allowUnixSockets` に
`~/Library/Application Support/rex` が要る (足すかは利用者が決める)。パスに空白が入るがクォートは要らない。

- `rex` を `excludedCommands` に置く形は取らない。rex の操作は `rex events --json | jq` のようにパイプと組み合わせるので、
  excluded にすると guard が照合外れで落とす
- 許可は再起動なしで次の Bash 呼び出しから効く (2026-10-10、Claude Code 2.1.296 で実測)。
  効いているかは sandbox 内から `rex ls` がセッション一覧を返すことで確かめる

## block と session_id の対応

この block は `REX_BLOCK`、記録は `context/<sid>.json` の `multiplexer.block`。一致すれば OK。
