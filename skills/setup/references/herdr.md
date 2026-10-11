# herdr の到達性と pane の対応

setup skill の 2 段目を herdr で行うときの手順｡

## 到達性

```sh
herdr status --json | jq -c '.server | {running, version}'
```

`running` が `true` でなければ NG｡herdr の server を起こしてもらう｡
daemon の到達確認 (`Reachable`) も同じ呼び出しを見る｡
`herdr integration install claude` (herdr 生成の SessionStart hook) は要らない｡
daemon は herdr の `agent_session` を読まない｡

## sandbox が socket を塞いでいるとき

JSON ではなく `Os { code: 1, kind: PermissionDenied }` が返るなら sandbox が socket を塞いでいる｡
`sandbox.network.allowUnixSockets` に socket の親ディレクトリ `~/.config/herdr` が要る (足すかは利用者が決める)｡

- unix socket はこのキーが単独で支配し､`filesystem.allowWrite` にも `network.allowedDomains` にも従わない
- キーは `sandbox.network` の下に置く｡`sandbox` 直下に書いても schema エラーにならず黙って無視される｡
  効いているかはシステムプロンプトの sandbox 要約の Network 節に `allowUnixSockets` が出るかで分かる
- Linux で全面許可するキーは boolean の `sandbox.network.allowAllUnixSockets`
- 許可はセッション開始時に評価される｡変えたら新しいセッションで `herdr agent list` が JSON を返すことを確かめる

## pane と session_id の対応

この pane は `HERDR_PANE_ID`､記録は `context/<sid>.json` の `multiplexer.pane`｡一致すれば OK｡

`multiplexer.pane` が `herdr pane list` のどの `pane_id` とも一致しないなら､別の workspace へ `pane move` した pane｡
ID が変わっても記録は起動時の値のままなので､その pane で Claude Code を起動し直してもらう
(`agents-daemon:agents-daemon` skill の operations.md「既知の制約」)｡
