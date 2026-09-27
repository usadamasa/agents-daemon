---
name: setup
description: >-
  agents-daemon plugin を install した直後､または動いていない疑いがあるときに､前提がそろっているかを確かめて
  足りない配線を足す｡herdr が入っていない・server が落ちている・このセッションが herdr の pane の外で動いている､
  statusline が stdin を `ingest-statusline` へ渡しておらず rate-limits / context の state file が無い､
  go や jq が無くてバイナリが建たない､といった状態を拾う｡「agents-daemon をセットアップして」「statusline を配線して」「herdr があるか確かめて」
  と言われたときにも使う｡
---

# setup

agents-daemon が動くのに要る前提を上から順に確かめ､欠けているものだけを直す｡
確かめるのは「設定が書いてあるか」ではなく「実際に動いた跡があるか」｡
statusline の中身を grep して判定しない (書き方は人によって違い､書いてあっても動いていないことがある)｡
例外は 3 段目の `ingest-statusline` を呼ぶ固定の 1 行だけ｡

各段の結果を次の 3 つのどれかで控え､最後にまとめて報告する｡

| 判定 | 意味 |
| ---- | ---- |
| OK | 前提がそろっている |
| NG | 欠けていて､daemon の役目のどれかが働かない｡直し方を示す |
| 情報 | 欠けているが､利用者の環境ではそれが正常なこともある｡直さず､何が効かなくなるかだけ伝える |

NG の段は直し方を示すが､利用者のファイル (statusline のスクリプト､settings.json) を書き換える前に必ず確認を取る｡

## 1. コマンドの有無

```sh
command -v herdr go jq
```

- `herdr`: 無ければ NG｡daemon は herdr の CLI で pane の画面を読み､プロンプトを送る｡herdr の代わりになる経路は無い｡<https://herdr.dev> を案内する｡
- `go`: 無ければ NG｡SessionStart hook が plugin のソースから `go build` する｡
  hook は Claude Code を起動したシェルの PATH で探すので､入れた後は Claude Code を起動し直す｡
- `jq`: 無ければ NG｡hook が plugin.json の version と hook 入力の JSON を読むのに使う｡

## 2. herdr の到達性と､このセッションの pane

```sh
herdr status --json | jq -c '.server | {running, version}'
```

`running` が `true` でなければ NG｡herdr の server を起こしてもらう｡daemon の到達確認 (`Reachable`) も同じ呼び出しを見る｡

続けて､このセッションが herdr の pane の中で動いていて､daemon から見えるかを確かめる｡
`HERDR_ENV` が立っているだけでは足りない｡daemon は `agent_session` で pane とセッションを対応付ける｡

```sh
sid=$("${CLAUDE_PLUGIN_ROOT}/scripts/get-session-id.sh")
herdr pane list | jq -c --arg sid "$sid" \
  '[.result.panes[] | select(.agent_session.value? == $sid) | {pane_id, agent}]'
```

- 1 件出れば OK｡
- 空なら NG｡herdr の外 (素のターミナル､IDE) で動いているか､herdr がまだ agent を認識していない｡
  herdr の pane の中で Claude Code を起動し直してもらう｡
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

### statusline へ足す

`context/<sid>.json` が NG なら､原因は 2 つに絞れる｡順に見る｡

1. バイナリが無い: 先に 4 段目を確かめる｡無ければそれが原因で､statusline 側は直さない
   (build が終われば次の描画から書かれる)｡
2. statusline が `ingest-statusline` を呼んでいない: statusline のスクリプトに
   `agents-daemon" ingest-statusline` の行があるかを見る｡呼ぶ行は固定の 1 行なので､ここだけは grep で判定してよい｡
   無ければ [statusline.md](references/statusline.md) を `Read` で開いて従う｡
   statusline の設定の置き場の調べ方､足す 1 行､書き換えた後の確かめ方がある｡

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

pane 一覧､セッションごとの rate-limits の鮮度､config.json の実効値､daemon の稼働を出す｡
`daemon は起動していません` なら､次のセッションの SessionStart で起きる｡
すぐ起こしたいときも､Claude の Bash からは起こさない｡Bash が sandbox の中で動く環境では､
そこから起こした daemon が sandbox の制限を引き継ぐ｡代わりに､利用者に次の 1 行をプロンプトへ打ってもらう｡

```sh
! "${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon" daemon --ensure
```

## 報告

次の形でまとめる｡

```
agents-daemon setup

- コマンド: herdr / go / jq … OK | NG (<無いもの>)
- herdr: server <running | 停止>､このセッションの pane <pane_id | 見つからない>
- statusline: context <OK (n 秒前) | NG>､rate-limits <OK | 無し (理由)>
- バイナリ: <version | NG (build.log の要点)>
- doctor: <要点>
- 直したこと: <書き換えたファイルと中身の要約 | なし>
- 残っていること: <利用者の作業 | なし>
```

context の自動 compact は既定で off｡使うかを聞き､
使うなら `${XDG_CONFIG_HOME:-$HOME/.config}/agents-daemon/config.json` に
`{"compactAutoEnabled": true}` を書く案を示す (既存のファイルがあればキーを足す)｡他の設定キーは
`agents-daemon:agents-daemon` skill の [operations.md](../agents-daemon/references/operations.md) にある｡
