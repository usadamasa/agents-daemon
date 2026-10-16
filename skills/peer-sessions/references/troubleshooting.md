# 送ったのに届かないとき

## 機能そのものが無い

`ListAgents` も `/list-agents` も無い、`printenv CLAUDE_CODE_MESSAGING_SOCKET` が空なら、そのセッションでは
機能が無効になっている。ハーネス側の設定の問題なので、使い方ではなく `agents-daemon:setup` skill の
「peer messaging」の段で切り分ける。

## 受信側の扱い

送ったのに相手が反応しないときは、受信側の `crossSessionInbound` を見る。値は `accept` /
`hold` / `refuse`。未設定なら送信側と受信側の permission mode のクラスで決まる。

| 受信側 | 送信側 | 結果 |
| ---- | ---- | ---- |
| prompt する側 (auto、acceptEdits、dontAsk、default) | prompt する側 | 配送 |
| prompt する側 | bypassPermissions | 保留 |
| bypassPermissions | bypassPermissions | 配送 |
| bypassPermissions | prompt する側 | 保留 |

保留は相手の端末に承認ダイアログとして出て、既定 5 分で破棄される。保留か拒否で届かなかった
ときは送信側に配送通知が来て、届かなかった旨とセッション名が書かれる。通知が無いまま返事が
無いなら、相手は受け取っていて作業中か、本文の「peer は素のセッションとして立てる」の片道の状態にある。

## 症状と対処

| 症状 | 見るところ |
| ---- | ---- |
| `ListAgents` も `/list-agents` も無い | 上の「機能そのものが無い」 |
| 宛先を拒否される | listing の名前をそのまま使っているか。同名が 2 行なら `[ref]` を付ける |
| 送ったのに相手が動かない | 配送通知が来ていれば「受信側の扱い」。来ていなければ各 reference の「送ったのに相手が動き出さないとき」 |
| idle 通知だけ届いて本文が来ない | 相手が `SendMessage` で返していないか、返す手段が無い (本文の「peer は素のセッションとして立てる」) |
| `herdr agent list` が `PermissionDenied`、`rex` が `connect: operation not permitted` | sandbox が多重化ツールの socket を塞いでいる。`agents-daemon:setup` skill の 2 段目 |
| `pane_not_found` | `herdr.md` の「background job から叩くとき」 |
| `agent_prompt_stalled` | `herdr.md` の「送ったのに相手が動き出さないとき」 |
| `block target "<label>" not found` | `rex.md` の「症状と対処」 |
| rex の症状が `rex.md` の表に無い | `rex.md` の「使い方・不具合・新機能の情報源」。feedback を `search_feedback` で引く |
