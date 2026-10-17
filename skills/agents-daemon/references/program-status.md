# rex の program_status (OSC 7501)

rex では Claude Code 自身が OSC 7501 で状態を報告する。daemon は `block call -b BLOCK program_status` の
`records` から Agent と `agent_status` を決める (`internal/rexcli` の `toPane`)。protocol の仕様は
[rex の docs](https://www.superlogical.com/rex/docs/build/program-status)。Claude Code がどの状況でどの値を出すかは
docs に無く、2.1.302 のバイナリと実測 (2026-10-17、rex preview.15、#69) から読んだもの。版が変われば測り直す。

<!-- cc-premise: 2.1.302 osc7501 state / app / kind の値と画面の状況の対応 (バイナリの builder と実測) -->
<!-- rex-premise: preview.15 program_status rex は process 名から record を補わない。owner.pid が record に付く -->

## 状況と record の対応

| Claude Code の状況 | `state` | `kind` | `agent_status` |
| ---- | ---- | ---- | ---- |
| 起動直後 (数秒以内に自己申告が来る)、利用者が止めた (Esc)、permission prompt を断った後 | `idle` | - | `idle` |
| 作業中 (`msg` は今の作業、`progress` はタスクの完了率) | `working` | - | `working` |
| ターンを終えて入力待ち (`msg` は要約) | `done` | - | `done` |
| permission prompt、sandbox の network 要求、worker の要求 (`msg` は `approve <tool>: <command>`) | `blocked` | `permission` | `blocked` |
| 利用者の入力待ち (MCP の elicitation など。AskUserQuestion がここに入るかは未確認) | `blocked` | `question` | `blocked` |
| 認証失敗でターンが終わり、まだ sign in していない | `blocked` | `auth` | `blocked` |
| その他の dialog (trust dialog、Session paused など) | `blocked` | 無し | `blocked` |
| API error でターンが終わった (`msg` は失敗の理由) | `error` | - | `unknown` |
| shell だけの block | `records: []` | - | Claude agent ではない |

`done` と `idle` はどちらも入力待ち。`blocked` は利用者の操作待ちで、daemon は打ち込まない。
`error` と `blocked` を上限検知に使う案は #77。

## record を使う条件

- root record (`id` が無いか空) だけ。`id` 付きは subagent のもの。
- `app` が `claude-code`。Claude Code は固定でこれを出す
  (preview.13 までの rex が process 名から補っていた `claude` は preview.15 では来ない)。
- record の `owner.pid` が前景 process の pid と一致する。仕様では `done` と `error` は process の終了と shell prompt
  を生き延びる。そのため Claude Code が SIGKILL で終わると素の shell に `done` が残る (実測。`/exit` は `state=clear` で消す)。
- 条件を満たす record が無くても、前景の `argv0` (basename) が `claude` なら Claude agent として `unknown`。

## payload の書式

`key=value` を `:` で繋ぎ、終端は BEL (`state=idle:app=claude-code`。`title` と `msg` は base64)。
`agents-daemon simulate` ([operations.md](operations.md)) の使い捨て block の script はこれを `printf` で出し、
`exec cat` で同じ pid のまま前景に居座って `owner.pid` の条件を満たす。
