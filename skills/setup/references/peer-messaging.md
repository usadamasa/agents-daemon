# peer messaging (cross-session messaging) の切り分け

Claude Code の `ListAgents` / `SendMessage` / `/list-agents` は、各セッションが unix domain socket を bind し、
Claude Code 本体がその上で配送する。使い方は `agents-daemon:peer-sessions` skill の担当で、ここは
「機能そのものが動いていない」ときの切り分けだけを扱う。

## 有効かどうかの確認

| 見るもの | 有効なときの出力 |
| ---- | ---- |
| `printenv CLAUDE_CODE_MESSAGING_SOCKET` (Bash ツールから) | `/tmp/cc-socks/<pid>.sock` |
| `agents-daemon doctor` の「peer messaging」の節 | `OK: socket があります` |
| `/status` の `Peer address` 行 | `uds:` 付きのパス |
| `/list-agents` | コマンドが応答し、他セッションが並ぶ |

socket のパスは `${XDG_RUNTIME_DIR:-${CLAUDE_CODE_TMPDIR:-/tmp}}/cc-socks/<pid>.sock` で決まる。
`<pid>` はセッションのプロセスのもので、Bash ツールの `$$` とは一致しない。

設定を変えた後の反映は新しいセッションで確認する。この確認には親セッションが要る。

- 入れ子で立てた `claude -p` では判定できない。`CLAUDE_CODE_TMPDIR` を渡されて socket 先が
  `/tmp/claude-<uid>/cc-socks/` へ移り、gate の可否によらず bind が seatbelt に拒否される
  (`doctor` が `⚠ socket が CLAUDE_CODE_TMPDIR … の下にあります` で指す状態。変数そのものは親セッションの
  Bash ツールにも来るので、変数の有無では見分けない)。debug ログには常に `Failed to listen` が出るので、
  `Skipped` の有無を対照実験で切り分けようとしても材料にならない。
- 端末多重化ツールのペインで立てたセッションは親相当で、`/tmp/cc-socks/` に bind できる。
  そこへ `SendMessage` で「ListAgents を呼んで結果と `printenv` の値を返せ」と頼む。gate は
  ツールの登録も支配するので、相手側でツールが生えていることが直接の証拠になる。
  立て方は `agents-daemon:peer-sessions` skill。

## 症状別の切り分け

### ツールもコマンドも生えていない

feature flag の gate で機能全体が落ちている。この gate は ListAgents ツールの登録、`/list-agents`、
システムプロンプトの peer 説明、受信ポリシーをまとめて支配する。
`~/.claude/debug/<session>.txt` に痕跡が残る。

```
[uds-messaging] Skipped: cross-session messaging gate off
```

順に見る。

1. flag の評価そのものを止める env を置いていないか。
   `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` `DISABLE_TELEMETRY` `DO_NOT_TRACK`
   `DISABLE_GROWTHBOOK` のいずれかが非空だと gate は off のままになる。
   settings の `env` と、Claude Code を起動したシェルの環境の両方を見る。
2. Claude Code が v2.1.224 以降か、OS が macOS または Linux か。
   <!-- cc-premise: 2.1.236 peer-messaging messaging socket と ListAgents / SendMessage (notify_when_idle は 2.1.236) -->

3. `~/.claude/statsig/` の評価キャッシュが取れているか。`statsig.cached.evaluations.*` の
   `source` が `NetworkNotModified` か `Network` なら評価は届いている。

3 が古いままなら flag の評価が届いていない。上書きの手段はハーネスの settings の `env` 側にあり、
この plugin では扱わない (利用者の設定リポジトリの担当)。

### socket の bind が拒否される

debug ログが `Skipped` ではなく次の形で落ちている場合。

```
[uds-messaging] Failed to listen at /tmp/claude-<uid>/cc-socks/<pid>.sock
```

sandbox 化した子プロセスとして起動したセッション (入れ子で立てた `claude` など) は
`CLAUDE_CODE_TMPDIR` を渡される。このため socket 先が `/tmp/claude-<uid>/cc-socks/` へ移り、
seatbelt に拒否される。この構成では messaging を使えない。
親セッションと background job はどちらも `/tmp/cc-socks/` に bind できるので、
検証はそちらから行う。

### peer は見えるのに届かない

settings (`~/.claude/settings.json` とプロジェクトの `.claude/settings*.json`) を見る。

```sh
jq -c '{deny: [.permissions.deny[]? | select(test("^(SendMessage|ListAgents)$"))], inbound: .crossSessionInbound}' ~/.claude/settings.json
```

- `permissions.deny` に `SendMessage` や `ListAgents` が入っていないか。入れると
  subagent や agent team 宛の送信も一緒に消える。
- `crossSessionInbound` が `refuse` になっていないか。これは受信を全部落とす kill switch。
  未設定なら送信側と受信側の permission mode のクラスで決まる (`accept` / `hold` / `refuse`)。
  prompt する側 (auto、acceptEdits、dontAsk、default) 同士は配送、`bypassPermissions` が片側だけなら保留。
  保留は相手の端末に承認ダイアログとして出て、既定 5 分で破棄される。

### sandbox 内の Bash や hook から socket へ書けない

`sandbox.network.allowUnixSockets` に `/tmp/cc-socks` と `/private/tmp/cc-socks` の両方が要る
(macOS の `/tmp` は `/private/tmp` への symlink)。`filesystem.allowWrite` では通らない。
`SendMessage` ツール自体は Claude Code 本体が送るので、この設定とは無関係に動く。
