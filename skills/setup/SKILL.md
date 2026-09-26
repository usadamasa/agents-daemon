---
name: setup
description: >-
  agents-daemon plugin を install した直後､または動いていない疑いがあるときに､前提がそろっているかを確かめて
  足りない配線を足す｡herdr が入っていない・server が落ちている・このセッションが herdr の pane の外で動いている､
  statusline が rate-limits / context の state file を書いていない､go や jq が無くてバイナリが建たない､
  といった状態を拾う｡「agents-daemon をセットアップして」「statusline を配線して」「herdr があるか確かめて」
  と言われたときにも使う｡
---

# setup

agents-daemon が動くのに要る前提を上から順に確かめ､欠けているものだけを直す｡
確かめるのは「設定が書いてあるか」ではなく「実際に動いた跡があるか」｡
statusline の中身を grep して判定しない (書き方は人によって違い､書いてあっても動いていないことがある)｡

各段の結果を OK / NG / 情報 で控え､最後にまとめて報告する｡
NG の段は直し方を示すが､利用者のファイル (statusline のスクリプト､settings.json) を書き換える前に必ず確認を取る｡
## 1. コマンドの有無

```sh
command -v herdr go jq
```

- `herdr`: 無ければ NG｡daemon は herdr の CLI で pane を見て送信するので､代わりが無い｡<https://herdr.dev> を案内する｡
- `go`: 無ければ NG｡SessionStart hook が plugin のソースから `go build` する｡
  hook は Claude Code を起動したシェルの PATH で探すので､入れた後は Claude Code を起動し直す｡
- `jq`: 無ければ NG｡hook と下の statusline のスニペットが使う｡

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

## 3. statusline が state file を書いているか

置き場は `${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/`｡
statusline は描画のたびに `context/<session_id>.json` を書くので､
この skill を動かしているセッション自身のファイルがあり､`observed_at` が新しければ配線は生きている｡

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
| `rate-limits/<sid>.json` が無い | 情報｡NG にしない (下を参照) |

`rate-limits/` は stdin に `rate_limits.five_hour.resets_at` が来たときだけ書かれる｡
初回の API レスポンス前には来ないし､`rate_limits` が届かない契約 (Enterprise の seat など) ではずっと来ない｡
無いときは「上限からの自動再開のゲートは画面判定だけで動く」と伝えるに留める｡

### statusline へ足す

まず statusline がどこに設定されているかを見る｡後に書いたものが勝つ｡

```sh
for f in "$HOME/.claude/settings.json" .claude/settings.json .claude/settings.local.json; do
  [ -f "$f" ] && jq -c --arg f "$f" 'select(.statusLine) | {file: $f, statusLine}' "$f"
done
```

- `statusLine` がどこにも無い: statusline のスクリプトを新しく作り､`statusLine` に登録する案を示す｡
- `statusLine.command` がスクリプトを指している: そのスクリプトを Read し､stdin の JSON を変数へ読んでいる箇所の後ろに
  下のスニペットを足す案を示す｡既に `agents-daemon` の state を書く処理があるのにファイルができていないなら､
  足さずにその処理の置き場 (早期 return の後ろにある､書き込み先が違う､など) を調べる｡

どちらも差分を見せて確認を取ってから書き換える｡

スニペットは stdin の JSON が `$input` に入っている前提｡state file の書式は
[architecture.md](../agents-daemon/references/architecture.md) の「statusline が書く state」に従う｡

```sh
# agents-daemon の daemon が読む state file｡描画を壊さないよう失敗は握りつぶす
ad_state="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon"
ad_sid=$(jq -r '.session_id // empty' <<<"$input" 2>/dev/null)
if [ -n "$ad_sid" ]; then
  {
    ad_dir="$ad_state/context"
    mkdir -p "$ad_dir" &&
      ad_tmp=$(mktemp "$ad_dir/.context.XXXXXX") &&
      jq -c '{session_id, used_percentage: ((.context_window.used_percentage // 0) | round), observed_at: (now | floor)}' \
        <<<"$input" >"$ad_tmp" &&
      mv "$ad_tmp" "$ad_dir/$ad_sid.json"
  } 2>/dev/null || :
  # five_hour が null の描画 (初回・ウィンドウの切り替わり) では書かず､既存ファイルも残す
  if jq -e '.rate_limits.five_hour.resets_at // empty' <<<"$input" >/dev/null 2>&1; then
    {
      ad_dir="$ad_state/rate-limits"
      mkdir -p "$ad_dir" &&
        ad_tmp=$(mktemp "$ad_dir/.rate-limits.XXXXXX") &&
        jq -c '.rate_limits as $r
          | {five_hour: {used_percentage: ($r.five_hour.used_percentage // 0 | round), resets_at: $r.five_hour.resets_at}}
          + (if $r.seven_day.resets_at then {seven_day: {used_percentage: ($r.seven_day.used_percentage // 0 | round), resets_at: $r.seven_day.resets_at}} else {} end)
          + {observed_at: (now | floor), session_id}' \
          <<<"$input" >"$ad_tmp" &&
        mv "$ad_tmp" "$ad_dir/$ad_sid.json"
    } 2>/dev/null || :
  fi
fi
```

- statusline から plugin の中のスクリプト (`${CLAUDE_PLUGIN_ROOT}/...`) を呼ばせない｡plugin の install 先は version ごとに変わり､
  更新した日から黙って書かれなくなる｡スニペットは statusline 側へ直接置く｡
- 一時ファイルは書き込み先と同じディレクトリに作る｡別のファイルシステムだと `mv` が原子的でなくなり､daemon が書きかけを読む｡

足した後は statusline が 1 回描画されるのを待ってから (次の応答の後)､この段の確認をやり直す｡

## 4. バイナリ

```sh
bin="${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin"
ls -l "$bin/agents-daemon" "$bin/agents-daemon.version"
cat "$bin/agents-daemon.version"
```

- 両方あれば OK｡stamp の値を報告に書く｡
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
すぐ起こしたいときは､利用者に次の 1 行をプロンプトへ打ってもらう｡
Claude の Bash が sandbox の中で動く環境では､そこから起こした daemon が sandbox の制限を引き継ぐ｡

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

context の自動 compact は既定で off｡使うかを聞き､使うなら `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` に
`{"compactAutoEnabled": true}` を書く案を示す (既存のファイルがあればキーを足す)｡他の設定キーは
`agents-daemon:agents-daemon` skill の [operations.md](../agents-daemon/references/operations.md) にある｡
