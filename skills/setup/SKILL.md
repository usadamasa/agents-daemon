---
name: setup
description: >-
  agents-daemon plugin を install した直後、または動いていない疑いがあるときに、前提 (rex、go と jq、
  statusline の配線、バイナリ、peer messaging、`dream forget` の sandbox 除外、タスクのツール) がそろっているかを確かめて
  足りない配線を足す。
  `doctor` で `session_id=(なし)`、state file が無い、sandbox 内から rex の socket に繋がらない、
  `ListAgents` が無い、`dream forget` が `operation not permitted` で消せない、といった症状でも引く。
  「agents-daemon をセットアップして」「statusline を配線して」と言われたときにも使う。
---

# setup

agents-daemon が動くのに要る前提を上から順に確かめ、欠けているものだけを直す。
確かめるのは「設定が書いてあるか」ではなく「実際に動いた跡があるか」。
statusline の中身を grep して判定しない (書き方は人によって違い、書いてあっても動いていないことがある)。
例外は 3 段目の `statusLine.command` の値と `ingest-statusline` を呼ぶ固定の 1 行だけ。

各段の結果を次の 3 つのどれかで控え、最後にまとめて報告する。

| 判定 | 意味 |
| ---- | ---- |
| OK | 前提がそろっている |
| NG | 欠けていて、daemon の役目のどれかが働かない。直し方を示す |
| 情報 | 欠けているが、利用者の環境ではそれが正常なこともある。直さず、何が効かなくなるかだけ伝える |

NG の段は直し方を示すが、利用者のファイル (statusline のスクリプト、settings.json) を書き換える前に必ず確認を取る。

## 1. コマンドの有無

```sh
command -v rex go jq
```

- `rex`: 無ければ NG。daemon は rex の CLI で block の画面を読み、プロンプトを送る。
- `go`: 無ければ NG。SessionStart hook が plugin のソースから `go build` する。
  hook は Claude Code を起動したシェルの PATH で探すので、入れた後は Claude Code を起動し直す。
- `jq`: 無ければ NG。hook が plugin.json の version と hook 入力の JSON を読むのに使う。

## 2. rex の到達性と、このセッションの block

先に rex の中かを確かめる。

```sh
printenv REX_SESSION
```

値が出なければ NG。rex の外 (素のターミナル、IDE) で動いていて、daemon は `REX_SESSION` の無いセッションの
SessionStart では起きない (statusline は動く)。rex の block の中で Claude Code を起動し直してもらう。
到達性と sandbox の socket 許可の手順は [rex.md](references/rex.md) にあり、`Read` で開いて従う。

### このセッションの block と session_id の対応

daemon は statusline (バイナリの `statusline` か `ingest-statusline`) が `context/<sid>.json` の `multiplexer` に書いた
場所から session_id を引く。このセッションの場所とその記録が一致するかを確かめる。

```sh
sid=$("${CLAUDE_PLUGIN_ROOT}/scripts/get-session-id.sh")
state="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"
printf 'REX_BLOCK=%s\n' "${REX_BLOCK:-}"
jq -c '.multiplexer' "$state/context/$sid.json"
```

- `REX_BLOCK` が空なら NG。`REX_SESSION` はあるのに `REX_BLOCK` が無いのは、block の外で起動した
  プロセス (`rex run` を経ない起動) で、この段では直せない。rex の block の中で Claude Code を起動し直してもらう。
- `multiplexer.block` が `REX_BLOCK` と一致すれば OK。
- `context/<sid>.json` が無いなら、この段ではなく 3 段目の NG (statusline の配線) として扱う。
- ファイルはあるのに `multiplexer` が `null` なら、statusline が呼ぶバイナリが `multiplexer` を書く前の版。
  4 段目のバイナリを建て直せば次の描画から書かれる。
- `get-session-id.sh` が失敗したら `CLAUDE_CODE_SESSION_ID` が来ていない。ここから先のセッション単位の確認はできないので、その旨を報告に残す。

## 3. statusline が state file を書かせているか

置き場は `${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/`。
配線ができていれば `context/<session_id>.json` が描画のたびに書き直されるので、
この skill を動かしているセッションのファイルがあり、`observed_at` も新しい。

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
| `context/<sid>.json` があり、`age_seconds` が数分以内 | OK |
| `context/<sid>.json` が無い、または古い | NG。下の「statusline へ足す」へ進む |
| `rate-limits/<sid>.json` がある | OK |
| `rate-limits/<sid>.json` が無い | 情報 (下を参照) |

`rate-limits/` は stdin に `rate_limits.five_hour.resets_at` が来たときだけ書かれる (出し分けはバイナリの側)。
初回の API レスポンス前には来ないし、`rate_limits` が届かない契約 (Enterprise の seat など) ではずっと来ない。
無いときは「上限からの自動再開のゲートは画面判定だけで動く」と伝えるに留める。

### statusline を配線する

`context/<sid>.json` が NG なら、原因は 2 つに絞れる。順に見る。

1. バイナリが無い: 先に 4 段目を確かめる。無ければそれが原因で、statusline 側は直さない
   (build が終われば次の描画から書かれる)。
2. statusline が配線されていない: `statusLine.command` がバイナリの `statusline` を指しているか、
   自前のスクリプトに `agents-daemon" ingest-statusline` の行があるかを見る。どちらも固定の文字列なので、
   ここだけは grep で判定してよい。どちらも無ければ [statusline.md](references/statusline.md) を `Read` で
   開いて従う。配線の 2 通り (`statusLine.command` をバイナリに向ける / 自前の statusline に 1 行足す)、
   statusline の設定の置き場の調べ方、書き換えた後の確かめ方がある。

## 4. バイナリ

```sh
bin="${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin"
ls -l "$bin/agents-daemon" "$bin/agents-daemon.version"
cat "$bin/agents-daemon.version"
```

- 両方あれば OK。`agents-daemon.version` (建てた元の plugin version を書いた stamp) の値を報告に書く。
- 無ければ NG。`${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/logs/build.log` の末尾を読む。
  install 直後のセッションなら build がまだ走っている途中かもしれない。`go not found in PATH` なら 1 段目へ戻る。
  それ以外の失敗は `agents-daemon:agents-daemon` skill の「daemon が起きない・バイナリがビルドされない」から追う。

## 5. doctor

バイナリがあるときだけ、最後にまとめとして回す。PATH には入っていないので絶対パスで呼ぶ。

```sh
"${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" doctor
```

pane 一覧、セッションごとの rate-limits の鮮度、config.json の実効値、daemon の稼働、peer messaging の socket を出す。
`daemon は起動していません` なら、次のセッションの SessionStart で起きる。
すぐ起こしたいときも、Claude の Bash からは起こさない。Bash が sandbox の中で動く環境では、
そこから起こした daemon が sandbox の制限を引き継ぐ。代わりに、利用者に次の 1 行をプロンプトへ打ってもらう。

```sh
! "${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" daemon --ensure
```

## 6. peer messaging

別セッション (peer) へ作業を渡す経路 (`agents-daemon:peer-sessions` skill) がこのセッションで使えるかを、
同じ場で確かめる。daemon のプロセスはこの経路に関わらない。

```sh
printenv CLAUDE_CODE_MESSAGING_SOCKET
```

| 結果 | 判定 |
| ---- | ---- |
| `/tmp/cc-socks/<pid>.sock` が出て、`doctor` の「peer messaging」が `OK: socket があります` | OK |
| 空 (exit 1)、または `ListAgents` ツールも `/list-agents` も無い | 情報。このセッションでは peer へ渡せない。[peer-messaging.md](references/peer-messaging.md) を `Read` で開いて切り分ける |
| `doctor` が `⚠ socket が CLAUDE_CODE_TMPDIR … の下にあります` を出す | 情報。入れ子で起動したセッションで、bind できない構成。親セッションから確かめる (同上) |

バイナリが無い、または 2 段目で多重化ツールに到達できず `doctor` がこの節まで進まないときは、`printenv` の値と
socket ファイルの有無 (`ls -l "$CLAUDE_CODE_MESSAGING_SOCKET"`) だけで判定する。
`CLAUDE_CODE_TMPDIR` 自体は親セッションの Bash ツールにも来る (sandbox が渡す) ので、
変数があるだけでは入れ子と判断しない。

## 7. dream forget の sandbox 除外

`agents-daemon:dream` の `dream forget` は auto memory を消す。`~/.claude/projects/**` は sandbox の保護下にあるので、
この 1 コマンドだけを sandbox の外に出す行を settings に足す。dream を使わないなら不要で、情報に留める。

```sh
rg -n 'agents-daemon dream forget' ~/.claude/settings.json
```

行が無ければ NG。足す行、照合の規則、効いているかの確かめ方は [dream-forget.md](references/dream-forget.md) を
`Read` で開いて従う。

## 8. タスクのツール

タスクのボード (`/board`) は TaskCreate / TaskUpdate / TaskList / TodoWrite の呼び出しを PostToolUse hook で記録して描く。
このセッションのツールにそれらがあるかを見る。

<!-- cc-premise: 2.1.302 tools Task 系ツールは env の CLAUDE_CODE_ENABLE_TODO_TOOLS=1 が無いと載らない (interactive と -p の両方で実測) -->

| 結果 | 判定 |
| ---- | ---- |
| TaskCreate か TodoWrite がある | OK |
| どちらも無い (`TaskStop` だけ) | 情報。ボードにはタスクが載らず、セッションごとのカード (今の作業か、セッションの名前) だけになる。使うなら settings の `env` に `"CLAUDE_CODE_ENABLE_TODO_TOOLS": "1"` を足し、Claude Code を起動し直す |

## 報告

次の形でまとめる。

```
agents-daemon setup

- コマンド: rex / go / jq … OK | NG (<無いもの>)
- rex: <REX_SESSION あり | 無し>、server <running | 停止>
- このセッションの block: <block_id | 見つからない | 記録なし (理由)>
- statusline: context <OK (n 秒前) | NG>、rate-limits <OK | 無し (理由)>
- バイナリ: <version | NG (build.log の要点)>
- doctor: <要点>
- peer messaging: <OK | 無効 (切り分けの結果) | 判定できず (理由)>
- dream forget の sandbox 除外: <OK | NG (足す行) | 情報 (dream を使わない)>
- タスクのツール: <OK | 情報 (CLAUDE_CODE_ENABLE_TODO_TOOLS が無い)>
- 直したこと: <書き換えたファイルと中身の要約 | なし>
- 残っていること: <利用者の作業 | なし>
```

設定キーは `agents-daemon:agents-daemon` skill の [operations.md](../agents-daemon/references/operations.md) にある。
既定値で動くので、聞かれなければ config.json は作らない。
