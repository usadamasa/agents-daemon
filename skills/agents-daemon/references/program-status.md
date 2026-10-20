# rex の program_status (OSC 7501)

rex では Claude Code 自身が OSC 7501 で状態を報告する。daemon は `block call -b BLOCK program_status` の
`records` から Agent と `agent_status` を決める (`internal/rexcli` の `toPane`)。protocol の仕様は
[rex の docs](https://www.superlogical.com/rex/docs/build/program-status)。Claude Code がどの状況でどの値を出すかは
docs に無く、2.1.302 のバイナリと実測 (2026-10-17、rex preview.15、#69) から読んだもの。版が変われば測り直す。

<!-- cc-premise: 2.1.302 osc7501 state / app / kind の値と画面の状況の対応 (バイナリの builder と実測) -->
<!-- rex-premise: preview.16 program_status rex は process 名から record を補わない。owner.pid が record に付く -->

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
| API error でターンが終わった (`msg` は失敗の理由) | `error` | - | `error` |
| shell だけの block | `records: []` | - | Claude agent ではない |

`done` と `idle` はどちらも入力待ち。`kind` と `msg` は `Pane` の `AgentKind` / `AgentMessage` に写し、
`status.json` (`agent_kind` / `agent_message`)、`status`、`doctor` の pane 行、`inspect --pane` の `record:` 行、
daemon.log の検知と送信の行に `agent_status=… kind=… msg="…"` の形で出す。
タスクのボードは、タスクの記録が無いセッションのカードの見出しに `msg` を出す (無ければセッション名か cwd の末尾)。
`unknown` の pane のノードは working に倒さず、`unknown` の状態として idle の節に出す。

`blocked` のうち kind が `permission` / `question` / `auth` の pane には、待機明けでも再開を送らない
(esc は prompt を断り、Enter は承認する。`internal/monitor` の `waitingOnPrompt`)。kind の無い `blocked` (dialog 一般) は
上限の画面がこの形で来るかを実測していないので送信を止めない。上限到達時の record の実測と、
`error` / `blocked` を画面読み取りの前段ゲートにする案は #77。

## record を使う条件

- root record (`id` が無いか空) だけ。`id` 付きは subagent のもの。
- `app` が `claude-code`。Claude Code は固定でこれを出す
  (preview.13 までの rex が process 名から補っていた `claude` は preview.15 では来ない)。
- record の `owner.pid` が `foreground` の process の pid と一致する。仕様では `done` と `error` は process の終了と shell prompt
  を生き延びる。そのため Claude Code が SIGKILL で終わると素の shell に `done` が残る (実測。`/exit` は `state=clear` で消す)。
- 条件を満たす record が無くても、`foreground` の `argv0` (basename) が `claude` なら Claude agent として `unknown`。
  record があるのに使えなかったときは理由を daemon のログ (`agent_status を判定できません`) と `doctor` の
  pane 一覧 (`unknown の理由`) に出す。`owner` は rex 独自の欄なので、rex の版が変わって欄が消えると全 pane が
  `unknown` になる。その状態を黙って通さないための出口。

## payload の書式

`key=value` を `:` で繋ぎ、終端は BEL (`state=idle:app=claude-code`。`title` と `msg` は base64)。
`agents-daemon simulate` ([operations.md](operations.md)) の使い捨て block の script はこれを `printf` で出し、
`exec cat` で同じ pid のまま foreground に居座って `owner.pid` の条件を満たす。
