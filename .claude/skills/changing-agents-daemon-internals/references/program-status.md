# Claude Code が rex に見せる状態の実測

Claude Code が OSC 7501 で申告する `program_status` の record と、block の画面のモードを、状況ごとに測った値。
どちらも Claude Code と rex の docs に無く、バイナリの読みと実測から取った。版が変われば測り直す。
record から Agent と `agent_status` を決める条件は `rex/blockstate` の `ToPane`、payload の書式は
`simulate/host.go` が持つ。protocol の仕様は [rex の docs](https://www.superlogical.com/rex/docs/build/program-status)。

## 状況と record の対応

<!-- cc-premise: 2.1.316 osc7501 state / app / kind の値と画面の状況の対応 (バイナリの builder と実測) -->
<!-- rex-premise: preview.37 program_status rex は process 名から record を補わない。owner.pid が record に付く -->

2.1.302 のバイナリと実測 (2026-10-17、rex preview.15) から読み、2.1.316 のバイナリで読み直したもの。

| Claude Code の状況 | `state` | `kind` | `agent_status` |
| ---- | ---- | ---- | ---- |
| 起動直後 (数秒以内に自己申告が来る)、利用者が止めた (Esc)、permission prompt を断った後 | `idle` | - | `idle` |
| 作業中 (`msg` は今の作業、`progress` はタスクの完了率) | `working` | - | `working` |
| ターンを終えて入力待ち (`msg` は要約) | `done` | - | `done` |
| permission prompt、sandbox の network 要求、worker の要求 (`msg` は `approve <tool>: <command>`) | `blocked` | `permission` | `blocked` |
| 利用者の入力待ち (MCP の elicitation など。AskUserQuestion がここに入るかは未確認) | `blocked` | `question` | `blocked` |
| 認証失敗でターンが終わり、まだ sign in していない | `blocked` | `auth` | `blocked` |
| その他の dialog (trust dialog、Session paused など。Session paused の `msg` は `dialog open`) | `blocked` | 無し | `blocked` |
| API error でターンが終わった (`msg` は失敗の理由の 1 行目) | `error` | - | `error` |
| shell だけの block | `records: []` | - | Claude agent ではない |

kind の無い `blocked` (dialog 一般) で上限の画面が来るかは測っていないので、daemon はこの形では送信を止めない。

## 上限で止まった pane の record

<!-- cc-premise: 2.1.316 osc7501 上限で止まった turn の state (バイナリの読み。実測は lower-priority の 1 件だけ) -->

2.1.316 のバイナリでは、state を決める関数の入力は turn の状態と最後の turn の終わり方だけで、auto-continue の
予約の有無は入らない。上限 (429) で止まった turn は API error の turn として終わるので、次の turn が始まるまで
`error` で、`msg` は上限の文 (`You've hit your …`) の 1 行目になる。

| 状況 | `state` | 根拠 |
| ---- | ---- | ---- |
| 5 時間上限で turn が止まり、auto-continue が予約している | `error` | バイナリの読み |
| 同じく、予約が無い・取り消された・止まった | `error` | バイナリの読み |
| lower-priority mode で容量を待っている (`Working at lower priority · waiting for capacity`) | `working` (request の中の retry 待ち) | バイナリの読み |
| session limit の後に lower-priority mode へ入った画面 (2026-10-25、limit-events に 1 件) | `idle` | 実測。バイナリの読みでは説明できていない |
| 一時エラー (過負荷、5xx) で retry を諦めた | `error` (`msg` は `API Error: …`) | バイナリの読み |

daemon が record から読むのは「`working` なら触らない」だけで、上限かどうかは transcript で決める (`monitor` の
package doc)。そのため実測の `idle` の 1 件も取りこぼさない。上限で止まった pane が `working` のまま申告される
食い違いは `suppressed-working` として limit-events に残る。同じ turn の終わりで StopFailure hook も走るが、
record の `error` は turn の結果を、StopFailure は API error の応答を見るので、条件は同じではない。
並べて読む手順は [verification.md](verification.md) の「次に上限を踏んだとき」。

## 画面のモード

<!-- cc-premise: 2.1.302 limit-screen Claude Code は alternate screen (?1049h) で描く -->
<!-- cc-premise: 2.1.304 limit-screen 起動直後の trust dialog は primary screen、本体の画面は alternate (?1049)。/exit で primary に戻す -->

Claude Code は alternate screen で描くので、Claude の block の `format` は可視の行数で頭打ちになる
(24 行の block で 24 行)。block を小さく分割していると、`readLines` を増やしても読める行は増えない。
待機明けに読む通知欄は画面の下端にあるので、高さの下限は無い。

使い捨ての block で Claude Code を起こして測った `modes` の値 (2026-10-20、rex preview.16、Claude Code 2.1.304)。

| Claude Code の状況 | `screen` | 非既定の mode | `program_status` の record | `process` の foreground |
| ---- | ---- | ---- | ---- | ---- |
| 起動直後 (trust dialog) | `primary` | 無し | 無し | claude |
| 本体の画面 | `alternate` | 1049、2004、mouse 系 | `idle` | claude |
| `/exit` の直後 | `primary` | 無し (全部戻る) | 無し (`clear` で消える) | null |
| SIGKILL の直後 | `alternate` のまま | 残ったまま | `idle` が残る | null |

SIGKILL では画面も record も残るので、`screen` で Claude Code の終了は見分けられない。終了は `process` の
foreground の pid と record の owner の突き合わせで見る (`rex/blockstate`)。`screen` は「`format` が可視の行だけを
返す前提に乗っているか」を `doctor` の「rex の前提」で点検するためだけに使う。
