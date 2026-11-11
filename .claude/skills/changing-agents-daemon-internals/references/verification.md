# 検証用コマンドと実機での検証

利用者向けのコマンドの一覧は plugin の `agents-daemon:agents-daemon` skill の
[operations.md](../../../../skills/agents-daemon/references/operations.md)。ここには検知と送信を確かめる道具と、
実機で通すときの順序を置く。

## 検証用コマンド

```sh
agents-daemon doctor                    # 前提条件の診断 (pane ごとの transcript の末尾と daemon の扱いを含む)
agents-daemon inspect --pane <id>       # 生きた pane の session から transcript を引いて判定し、画面の通知欄を読む
agents-daemon inspect --file <path>     # 保存した画面テキストの通知欄だけを読む (transcript は引かない)
agents-daemon inspect --file <path> --session <id>  # 加えて、そのセッションの transcript で判定する
agents-daemon simulate                  # 使い捨て pane で検知から送信まで通す (transcript は内蔵の上限の entry)
agents-daemon simulate --turn-file <jsonl>  # transcript の末尾として読ませる entry を差し替える
agents-daemon simulate --send --keep    # 実際に送信し、pane を残して目で見る
agents-daemon daemon --foreground --dry-run  # このプロセスで回し、送信と起こした session の後片付けだけをしない
```

`statusLine.command` から呼ばれる `statusline` は stdin の JSON を受けて state file を書き、描画を stdout に出す。
手で叩くなら `COLUMNS=200 agents-daemon statusline < in.json` で渡す (COLUMNS が無ければ full の 3 行)。
自前の statusline から呼ぶ `ingest-statusline` は state file を書くだけで stdout には何も出さない。
どちらも失敗しても exit 0 で終わる (理由は stderr)。

`inspect` は判定結果だけでなく、読んだ transcript の末尾の entry (`transcript`)、分類 (`kind`)、daemon の扱い
(`plan`: `wait` / `weekly-far` / `stale-turn` / `hard-cap` / `none`)、起床時刻の出所 (`wake`)、画面の走査した行と
通知欄の判定 (`hint`) を出す。検知がおかしいと疑ったときに、結論ではなく判断過程を見るためのもの。判断は daemon と
同じ関数 (`detect.ClassifyTurn`、`monitor.Decide`、`monitor.ComputeLimitWake`、`detect.ClassifyScreen`) を呼ぶ。

transcript を引くには「どのセッションか」が要る。`--pane` は pane から daemon と同じ順 (statusline の記録、無ければ
前景の pid で登録簿) で解決する。`--file` にはセッションが無いため `--session` で明示する。指定が無ければ
対象セッションが決まらない旨を出し、画面の通知欄の判定だけを報告する。

`simulate` は自分の block を分割して使い捨ての block を作り、Claude agent に見せかけ、limit 風の画面を書き込み、
内蔵の上限の entry (`--turn-file` で差し替え) を transcript の末尾として monitor に読ませて、デーモンと同じ判定を通す。
使い捨ての pane には本物の transcript が無いため。既定では送信しない。自分が作った block 以外には
決して触らない。`REX_SESSION` と `REX_BLOCK` が要る。`~/.claude` には書かない。

使い捨て block の前景は `/bin/sh <script> <screen>`。script は limit 風の画面を cat したあと
`exec cat -v` で居座り、daemon が送る esc・文字列・enter を解釈せずそのまま画面に書く (ESC は `^[`)。
shell を受け手にすると ESC の直後の文字を Meta として食う (zsh の `KEYTIMEOUT` 0.4 秒) など shell の解釈が混ざり、
`--send` で何が届いたかを画面で読めない。

| 自分の block | script の起動 | Claude agent に見せる手段 | 後始末 |
| ---- | ---- | ---- | ---- |
| `REX_BLOCK` の block | `rex split --shell=none -- /bin/sh …` で shell を挟まず直接 | script の先頭で Claude Code と同じ OSC 7501 (`state=idle:app=claude-code`) を `printf` し、`program_status` の record に載せる | `rex block close` |

登録が rex に載ったことを daemon と同じ経路 (`PaneGet` → `IsClaudeAgent`) で待ってから画面を読む。
rex の `capture` は可視領域しか返さないので、使い捨て block は自分の block の 40% の高さで割る。
OSC 7501 の書式と判定の条件は [program-status.md](program-status.md)。

`doctor` は rex の server への到達性、Claude pane の一覧 (`session_id` とその出どころ (statusline / registry)、
transcript の末尾の entry と分類と daemon の扱い、読めなければ ⚠ と理由、block の高さと screen) を出す。
rex の外 (`REX_SESSION` なし) では exit 1 で止まる。「rex CLI の前提」は自分の block (`REX_BLOCK`) に固定の
コマンドを打ち、コードが読む欄と exit code が rex の版で変わっていないかを `⚠` で名指しする (rex を上げた直後に
全 pane が unknown になったときの最初の手がかり)。末尾の「peer messaging」は
`CLAUDE_CODE_MESSAGING_SOCKET` と socket の有無で、Claude Code のセッション内 (Bash ツール) から呼んだときだけ
意味を持つ (切り分けは `agents-daemon:setup` skill の `references/peer-messaging.md`)。
`inspect --pane` の pane ID は block ID。

## 次に上限を踏んだとき

上限は起こさないと測れない。daemon は上限の出来事 (daemon.log に行を書くのと同じ遷移) のたびに、踏んだ瞬間の
材料を `$STATE/limit-events/<YYYY-MM-DD>.jsonl` へ 1 行の JSON で残す (`daemon/limitlog`、欄は package doc)。
上限を踏んだあとにこれを読んで、未測定のまま残っている次の問いに答える。

| 問い | 見る欄 |
| ---- | ---- |
| 上限の entry は今の版でも同じ形か (`error=rate_limit`、`quotaLimits.resetsAt`) | `transcript` (`error` / `quota_limits` / `text` / `version`) と `kind`。`reason` があれば読めなかった理由 |
| 上限到達時に Claude Code は record を何と申告するか | `record` (`agent_status` / `kind` / `msg`)。`working` のままなら `outcome` が `suppressed-working` の行が続く (書き込みの遅れか前提の崩れ) |
| 上限到達時に StopFailure が走ったか、transcript と食い違うか | `turn_failure` (`error` / `last_assistant_message` / `observed_at`)。欄が無ければ、最後の turn は API error で終わっていないか、hook が走っていないか、検知が hook の書き込みより先だった (同じ pane の次の出来事を見る)。食い違いは daemon.log にも 1 行出る |
| 待つかどうかと起床時刻の判断は正しかったか | `outcome` (`waiting` / `weekly-cap` / `stale-turn` / `hard-cap`)、`transcript.quota_limits.resets_at`、`wait_until` |
| 待機明けに送るキーの選択は正しかったか | `hint` と `screen` (送った tick だけ)。`geometry.rows` / `alternate_screen` で通知欄が画面に入っていたか |
| 設定を変えたら通知欄の判定が変わるか | `screen` を `inspect --file` に渡す (下) |

```sh
jq -c '{at, pane_id, outcome, kind, reason, record, wait_until}' "$STATE"/limit-events/*.jsonl  # 出来事の一覧
jq -c '.transcript | select(.) | {version, error, quota_limits, text}' "$STATE"/limit-events/*.jsonl  # 読んだ entry
jq -r 'select(.screen) | .screen' "$STATE"/limit-events/2026-10-21.jsonl > ./tmp/limit-screen.txt
agents-daemon inspect --file ./tmp/limit-screen.txt --session <session_id>  # 同じ画面と transcript で判定し直す
```

`screen` には上限の文言と通知欄がそのまま入っている。判定は transcript なので監視中の pane に出しても待機には
入らないが、待機明けのキー選択には効くので、`jq` で欄を選んで読み、画面に出すなら別のファイルへ落として
`inspect --file` に渡す。

## 実機での検証順序

誤検知しないことの確認を先に置く。検知できることの確認はその後。

1. `task install` (リポジトリの clone で、hook と同じ cache のパスへ建てる)。共有のバイナリを差し替えたくなければ
   worktree から `go run ./cmd/agents-daemon …` で代える
2. `doctor` — 前提が揃っているか。pane ごとに transcript の末尾を読めていること
3. `inspect --pane <自分の pane>` — 普通に終わった turn が `none` と判定されること
4. `inspect --file <画面のサンプル> --session <上限で止まったセッション>` — `limit` と判定され、起床時刻が entry の
   `resetsAt` 由来であること。手元に無ければ `simulate --turn-file` に `transcript/testdata` の entry を渡す
5. `daemon --foreground --dry-run` — 実 pane 相手に流し、誰にも送信せず誤爆もしないこと
6. `simulate` — 検知から送信までを end-to-end で通す

## 動いている daemon とかち合わずに試す

worktree で建てたバイナリを実機の rex で試すときは、利用者の daemon と state を分ける。

```sh
go build -o ./tmp/ad ./cmd/agents-daemon
cp ./tmp/ad ~/tmp/ad-<topic>
XDG_STATE_HOME=~/tmp/state-<topic> ~/tmp/ad-<topic> daemon --foreground
```

- `XDG_STATE_HOME` を分けると `daemon.lock` も分かれ、動いている daemon と並んで起きる。印のファイルを書くサブコマンド
  (`spawned-session mark` など) も同じ `XDG_STATE_HOME` で呼ぶ
- バイナリは `~/tmp` へ写して呼ぶ。worktree のパスは `github.com` を含み、Bash の guard が git の操作と見なして止める
- 済んだら `cc-rm tmp ~/tmp/ad-<topic> ~/tmp/state-<topic>` で消す

## 実機で通すこと

送信の経路と画面の読み取り元は、fake ではなく実機で 1 度は通す。多重化ツールの応答の解釈違いや
読み取り元の範囲 ([pane-io.md](pane-io.md) の「読み取り元」) は、`--dry-run` や `simulate` だけでは踏めない。
rex の送信が未測定なのは、まだ果たしていない箇所 (operations.md の「既知の制約」)。
上限の entry の形は実機の transcript から写したものを `transcript/testdata` に固定してあり (2.1.239 の seven_day、
2.1.251 の Fable、2.1.283 の Request rejected 429、2.1.289 の five_hour、2.1.309 の server_error)、
`detect` と `monitor` のテストの fixture もそこから写している。2.1.300 以降の実機で上限を踏んだ transcript はまだ無く、
次に踏んだときの limit-events で確かめる。
