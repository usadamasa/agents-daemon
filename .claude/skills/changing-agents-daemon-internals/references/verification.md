# 検証用コマンドと実機での検証

利用者向けのコマンドの一覧は plugin の `agents-daemon:agents-daemon` skill の
[operations.md](../../../../skills/agents-daemon/references/operations.md)。ここには検知と送信を確かめる道具と、
実機で通すときの順序を置く。

## 検証用コマンド

```sh
agents-daemon doctor                    # 前提条件の診断
agents-daemon inspect --pane <id>       # 生きた pane の画面を分類する
agents-daemon inspect --file <path>     # 保存した画面テキストを分類する
agents-daemon inspect --file <path> --session <id>  # ゲート判定に使うセッションを指定する
agents-daemon simulate                  # 使い捨て pane で検知から送信まで通す
agents-daemon simulate --send --keep    # 実際に送信し、pane を残して目で見る
agents-daemon daemon --foreground --dry-run  # このプロセスで回し、送信だけをしない
```

`statusLine.command` から呼ばれる `statusline` は stdin の JSON を受けて state file を書き、描画を stdout に出す。
手で叩くなら `COLUMNS=200 agents-daemon statusline < in.json` で渡す (COLUMNS が無ければ full の 3 行)。
自前の statusline から呼ぶ `ingest-statusline` は state file を書くだけで stdout には何も出さない。
どちらも失敗しても exit 0 で終わる (理由は stderr)。

`inspect` は判定結果だけでなく、走査した行・ゲートが開いているか・待機時刻をどの
情報源から得たかを出す。検知がおかしいと疑ったときに、結論ではなく判断過程を見るためのもの。

ゲート判定には「どのセッションの state を見るか」が要る。`--pane` を指定したときは
pane から解決する。`--file` にはセッションが無いため `--session` で明示する。
指定が無ければゲート判定を行わない旨を出力して、画面の分類だけを報告する。

`simulate` は自分の block を分割して使い捨ての block を作り、Claude agent に見せかけ、limit 風の画面を
書き込んで、デーモンと同じ判定を通す。既定では送信しない。自分が作った block 以外には
決して触らない。`REX_SESSION` と `REX_BLOCK` が要る。

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

`doctor` は rex の server への到達性、Claude pane の一覧 (`session_id` が引けなければ `(なし)`、block の高さと screen、
高さが `detect.MinDetectableRows` 未満なら ⚠) を出す。
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
| 上限到達時に Claude Code は record を何と申告するか (#77) | `record` (`agent_status` / `kind` / `msg`)。`working` のままか、`error` に msg が付くか |
| 上限到達時に StopFailure が走ったか、record と食い違うか (#77) | `turn_failure` (`error` / `last_assistant_message` / `observed_at`)。欄が無ければ、最後の turn は API error で終わっていないか、hook が走っていないか、検知が hook の書き込みより先だった (同じ pane の次の出来事を見る) |
| 小さい block で上限の文と `❯` の行が同時に見えるか、`/rate-limit-options` のメニューが出た状態の下限 (#39) | `geometry.rows` / `geometry.alternate_screen` と `screen` (判定した画面そのもの。上限の文が下から何行目か、メニューと入力行が入っているか) |
| ゲートと起床時刻の判断は正しかったか | `rate_limit` (own)、`account`、`wait_until`、`outcome` |
| 設定を変えたら判定が変わるか | `screen` を `inspect --file` に渡す (下) |
| lower-priority mode で作業中の pane を待機に入れていないか (#251) | `lower_priority` が true の行の `outcome` と `screen` |

```sh
jq -c '{at, pane_id, outcome, kind, record, turn_failure, geometry}' "$STATE"/limit-events/*.jsonl   # 出来事の一覧
jq -r 'select(.outcome == "waiting") | .screen' "$STATE"/limit-events/2026-10-21.jsonl > ./tmp/limit-screen.txt
agents-daemon inspect --file ./tmp/limit-screen.txt --session <session_id>             # 同じ画面を判定し直す
```

`screen` には上限の文言と reset 時刻がそのまま入っている。監視中の pane に `cat` すると daemon がそれを
limit と判定するので、`jq` で欄を選んで読み、画面に出すなら別のファイルへ落として `inspect --file` に渡す。

## 実機での検証順序

誤検知しないことの確認を先に置く。検知できることの確認はその後。

1. `task install` (リポジトリの clone で、hook と同じ cache のパスへ建てる)
2. `doctor` — 前提が揃っているか
3. `inspect --pane <自分の pane>` — 通常の画面が `none` と判定されること
4. `inspect --file <limit 画面のサンプル>` — `limit` と判定され、待機時刻が `resets_at` 由来であること
5. `daemon --foreground --dry-run` — 実 pane 相手に流し、誰にも送信せず誤爆もしないこと
6. `simulate` — 検知から送信までを end-to-end で通す

## 実機で通すこと

送信の経路と画面の読み取り元は、fake ではなく実機で 1 度は通す。多重化ツールの応答の解釈違いや
読み取り元の範囲 ([pane-io.md](pane-io.md) の「読み取り元」) は、`--dry-run` や `simulate` だけでは踏めない。
rex の送信が未測定なのは、まだ果たしていない箇所 (operations.md の「既知の制約」)。
上限の文言の分類は、実機で取得した表示を `detect/real_sample_test.go` に固定してある。
