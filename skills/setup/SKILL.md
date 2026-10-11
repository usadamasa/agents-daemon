---
name: setup
description: >-
  agents-daemon plugin を install した直後､または動いていない疑いがあるときに､前提がそろっているかを確かめて
  足りない配線を足す｡herdr も rex も入っていない・server が落ちている・このセッションが herdr の pane や
  rex の block の外で動いている､pane や block に session_id が紐づかない (`doctor` で `session_id=(なし)`)､
  statusline が配線されておらず (`statusLine.command` がバイナリの `statusline` を指していない､または自前の
  statusline が stdin を `ingest-statusline` へ渡していない) rate-limits / context の state file が無い､
  go や jq が無くてバイナリが建たない､sandbox 内から `herdr` が `PermissionDenied`・`rex` が
  `connect: operation not permitted` で落ちる､peer messaging (`ListAgents` / `SendMessage`) が無い・
  `CLAUDE_CODE_MESSAGING_SOCKET` が空､といった状態を拾う｡「agents-daemon をセットアップして」「statusline を配線して」
  「herdr があるか確かめて」「rex で動くか確かめて」と言われたときにも使う｡
---

# setup

agents-daemon が動くのに要る前提を上から順に確かめ､欠けているものだけを直す｡
確かめるのは「設定が書いてあるか」ではなく「実際に動いた跡があるか」｡
statusline の中身を grep して判定しない (書き方は人によって違い､書いてあっても動いていないことがある)｡
例外は 3 段目の `statusLine.command` の値と `ingest-statusline` を呼ぶ固定の 1 行だけ｡

各段の結果を次の 3 つのどれかで控え､最後にまとめて報告する｡

| 判定 | 意味 |
| ---- | ---- |
| OK | 前提がそろっている |
| NG | 欠けていて､daemon の役目のどれかが働かない｡直し方を示す |
| 情報 | 欠けているが､利用者の環境ではそれが正常なこともある｡直さず､何が効かなくなるかだけ伝える |

NG の段は直し方を示すが､利用者のファイル (statusline のスクリプト､settings.json) を書き換える前に必ず確認を取る｡

## 1. コマンドの有無

```sh
command -v herdr rex go jq
```

- `herdr` / `rex`: どちらか 1 つあれば OK､両方無ければ NG｡daemon は多重化ツールの CLI で pane の画面を読み､
  プロンプトを送る｡どちらを使うかは環境で決まる (`REX_SESSION` があれば rex､無ければ herdr)｡
  この skill を動かしているセッションが使う側が無ければ NG｡herdr なら <https://herdr.dev> を案内する｡
- `go`: 無ければ NG｡SessionStart hook が plugin のソースから `go build` する｡
  hook は Claude Code を起動したシェルの PATH で探すので､入れた後は Claude Code を起動し直す｡
- `jq`: 無ければ NG｡hook が plugin.json の version と hook 入力の JSON を読むのに使う｡

## 2. 多重化ツールの到達性と､このセッションの pane

先に backend を決める｡

```sh
printenv REX_SESSION
```

値が出れば rex､出なければ herdr｡daemon も同じ規則で選ぶ｡到達性の確かめ方だけが backend で違う｡

### 到達性

herdr:

```sh
herdr status --json | jq -c '.server | {running, version}'
```

`running` が `true` でなければ NG｡herdr の server を起こしてもらう｡daemon の到達確認 (`Reachable`) も同じ呼び出しを見る｡
`herdr integration install claude` (herdr 生成の SessionStart hook) は要らない｡
daemon は herdr の `agent_session` を読まない｡

JSON ではなく `Os { code: 1, kind: PermissionDenied }` が返るなら sandbox が socket を塞いでいる｡
`sandbox.network.allowUnixSockets` に socket の親ディレクトリ `~/.config/herdr` が要る (足すかは利用者が決める)｡
unix socket はこのキーが単独で支配し､`filesystem.allowWrite` にも `network.allowedDomains` にも従わない｡
キーは `sandbox.network` の下に置く｡`sandbox` 直下に書いても schema エラーにならず黙って無視される｡
効いているかはシステムプロンプトの sandbox 要約の Network 節に `allowUnixSockets` が出るかで分かる｡
Linux で全面許可するキーは boolean の `sandbox.network.allowAllUnixSockets`｡
許可はセッション開始時に評価されるので､変えたら新しいセッションで `herdr agent list` が JSON を返すことを確かめる｡

rex:

```sh
rex --autostart=false api call server.status | jq -c '{pid, session_count}'
```

失敗するか `pid` が無ければ NG｡rex の server を起こしてもらう｡daemon の到達確認 (`Reachable`) も同じ呼び出しを見る｡
`operation not permitted` で socket に繋がらないなら sandbox が止めている｡`sandbox.network.allowUnixSockets` に
`~/Library/Application Support/rex` が要る (足すかは利用者が決める)｡パスに空白が入るがクォートは要らない｡
`rex` を `excludedCommands` に置く形は取らない｡rex の操作は `rex events --json | jq` のように
パイプと組み合わせるので､excluded にすると guard が照合外れで落とす｡
rex の許可は再起動なしで次の Bash 呼び出しから効く (2026-10-10､Claude Code 2.1.296 で実測｡
herdr の「セッション開始時に評価」とは違う)｡
効いているかは sandbox 内から `rex ls` がセッション一覧を返すことで確かめる｡

### このセッションの pane (block) と session_id の対応

herdr も rex も､daemon は statusline (バイナリの `statusline` か `ingest-statusline`) が `context/<sid>.json` の
`multiplexer` に書いた場所 (herdr は `HERDR_PANE_ID`､rex は `REX_BLOCK`) から session_id を引く｡
このセッションの場所とその記録が一致するかを確かめる｡

```sh
sid=$("${CLAUDE_PLUGIN_ROOT}/scripts/get-session-id.sh")
state="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"
printf 'HERDR_PANE_ID=%s REX_BLOCK=%s\n' "${HERDR_PANE_ID:-}" "${REX_BLOCK:-}"
jq -c '.multiplexer' "$state/context/$sid.json"
```

- `multiplexer.kind` が backend と同じで､`multiplexer.pane` が `HERDR_PANE_ID` (herdr) または `multiplexer.block` が
  `REX_BLOCK` (rex) と一致すれば OK｡
- `HERDR_PANE_ID` / `REX_BLOCK` が両方空なら NG｡多重化ツールの外 (素のターミナル､IDE) で動いている｡
  herdr の pane か rex の block の中で Claude Code を起動し直してもらう｡
- `context/<sid>.json` が無いなら､この段ではなく 3 段目の NG (statusline の配線) として扱う｡
- ファイルはあるのに `multiplexer` が `null` なら､statusline が呼ぶバイナリが `multiplexer` を書く前の版｡
  4 段目のバイナリを建て直せば次の描画から書かれる｡
- herdr で `multiplexer.pane` が `pane list` のどの `pane_id` とも一致しないなら､別の workspace へ `pane move` した pane｡
  ID が変わっても記録は起動時の値のままなので､その pane で Claude Code を起動し直してもらう
  (`agents-daemon:agents-daemon` skill の operations.md「既知の制約」)｡
- `get-session-id.sh` が失敗したら `CLAUDE_CODE_SESSION_ID` が来ていない｡ここから先のセッション単位の確認はできないので､その旨を報告に残す｡

## 3. statusline が state file を書かせているか

置き場は `${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/`｡
配線ができていれば `context/<session_id>.json` が描画のたびに書き直されるので､
この skill を動かしているセッションのファイルがあり､`observed_at` も新しい｡

```sh
sid=$("${CLAUDE_PLUGIN_ROOT}/scripts/get-session-id.sh")
state="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"
jq -c --argjson now "$(date +%s)" '. + {age_seconds: ($now - .observed_at)}' "$state/context/$sid.json"
```

```sh
sid=$("${CLAUDE_PLUGIN_ROOT}/scripts/get-session-id.sh")
state="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"
jq -c . "$state/rate-limits/$sid.json"
```

| 結果 | 判定 |
| ---- | ---- |
| `context/<sid>.json` があり､`age_seconds` が数分以内 | OK |
| `context/<sid>.json` が無い､または古い | NG｡下の「statusline へ足す」へ進む |
| `rate-limits/<sid>.json` がある | OK |
| `rate-limits/<sid>.json` が無い | 情報 (下を参照) |

`rate-limits/` は stdin に `rate_limits.five_hour.resets_at` が来たときだけ書かれる (出し分けはバイナリの側)｡
初回の API レスポンス前には来ないし､`rate_limits` が届かない契約 (Enterprise の seat など) ではずっと来ない｡
無いときは「上限からの自動再開のゲートは画面判定だけで動く」と伝えるに留める｡

### statusline を配線する

`context/<sid>.json` が NG なら､原因は 2 つに絞れる｡順に見る｡

1. バイナリが無い: 先に 4 段目を確かめる｡無ければそれが原因で､statusline 側は直さない
   (build が終われば次の描画から書かれる)｡
2. statusline が配線されていない: `statusLine.command` がバイナリの `statusline` を指しているか､
   自前のスクリプトに `agents-daemon" ingest-statusline` の行があるかを見る｡どちらも固定の文字列なので､
   ここだけは grep で判定してよい｡どちらも無ければ [statusline.md](references/statusline.md) を `Read` で
   開いて従う｡配線の 2 通り (`statusLine.command` をバイナリに向ける / 自前の statusline に 1 行足す)､
   statusline の設定の置き場の調べ方､書き換えた後の確かめ方がある｡

## 4. バイナリ

```sh
bin="${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin"
ls -l "$bin/agents-daemon" "$bin/agents-daemon.version"
cat "$bin/agents-daemon.version"
```

- 両方あれば OK｡`agents-daemon.version` (建てた元の plugin version を書いた stamp) の値を報告に書く｡
- 無ければ NG｡`${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/logs/build.log` の末尾を読む｡
  install 直後のセッションなら build がまだ走っている途中かもしれない｡`go not found in PATH` なら 1 段目へ戻る｡
  それ以外の失敗は `agents-daemon:agents-daemon` skill の「daemon が起きない・バイナリがビルドされない」から追う｡

## 5. doctor

バイナリがあるときだけ､最後にまとめとして回す｡PATH には入っていないので絶対パスで呼ぶ｡

```sh
"${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" doctor
```

pane 一覧､セッションごとの rate-limits の鮮度､config.json の実効値､daemon の稼働､peer messaging の socket を出す｡
`daemon は起動していません` なら､次のセッションの SessionStart で起きる｡
すぐ起こしたいときも､Claude の Bash からは起こさない｡Bash が sandbox の中で動く環境では､
そこから起こした daemon が sandbox の制限を引き継ぐ｡代わりに､利用者に次の 1 行をプロンプトへ打ってもらう｡

```sh
! "${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" daemon --ensure
```

## 6. peer messaging

daemon の役目には関わらないが､別セッション (peer) へ作業を渡す経路 (`agents-daemon:peer-sessions` skill) が
このセッションで使えるかを､同じ場で確かめる｡

```sh
printenv CLAUDE_CODE_MESSAGING_SOCKET
```

| 結果 | 判定 |
| ---- | ---- |
| `/tmp/cc-socks/<pid>.sock` が出て､`doctor` の「peer messaging」が `OK: socket があります` | OK |
| 空 (exit 1)､または `ListAgents` ツールも `/list-agents` も無い | 情報｡このセッションでは peer へ渡せない｡[peer-messaging.md](references/peer-messaging.md) を `Read` で開いて切り分ける |
| `doctor` が `⚠ socket が CLAUDE_CODE_TMPDIR … の下にあります` を出す | 情報｡入れ子で起動したセッションで､bind できない構成｡親セッションから確かめる (同上) |

バイナリが無い､または 2 段目で多重化ツールに到達できず `doctor` がこの節まで進まないときは､`printenv` の値と
socket ファイルの有無 (`ls -l "$CLAUDE_CODE_MESSAGING_SOCKET"`) だけで判定する｡
`CLAUDE_CODE_TMPDIR` 自体は親セッションの Bash ツールにも来る (sandbox が渡す) ので､
変数があるだけでは入れ子と判断しない｡

## 報告

次の形でまとめる｡

```
agents-daemon setup

- コマンド: herdr / rex / go / jq … OK | NG (<無いもの>)
- backend: <herdr | rex>､server <running | 停止>
- このセッションの pane: <pane_id または block_id | 見つからない | 記録なし (理由)>
- statusline: context <OK (n 秒前) | NG>､rate-limits <OK | 無し (理由)>
- バイナリ: <version | NG (build.log の要点)>
- doctor: <要点>
- peer messaging: <OK | 無効 (切り分けの結果) | 判定できず (理由)>
- 直したこと: <書き換えたファイルと中身の要約 | なし>
- 残っていること: <利用者の作業 | なし>
```

context の自動 compact は既定で off｡使うかを聞き､
使うなら `${XDG_CONFIG_HOME:-$HOME/.config}/agents-daemon/config.json` に
`{"compactAutoEnabled": true}` を書く案を示す (既存のファイルがあればキーを足す)｡他の設定キーは
`agents-daemon:agents-daemon` skill の [operations.md](../agents-daemon/references/operations.md) にある｡
