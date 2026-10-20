# 実機の CLI の点検

自分の block に打つ固定のコマンドと、コードが読む欄、突き合わせる先。`internal/rexcli` が rex に打つコマンドと
同じ引数にしてある。コードが打つコマンドを変えたら、この表も同じ PR で変える。
パスは `internal/rexcli/` からの相対。実測を取り直したら `testdata/` に日付と版のディレクトリを足す
(`testdata/README.md`)。

## doctor が見る行

`go run ./cmd/agents-daemon doctor` の「rex CLI の前提」の節が、自分の block (`REX_BLOCK`) に打って
欄の有無と exit code を名指しで出す (`internal/rexcli/premise.go`)。人が打ち直す必要は無い。
節の `⚠` の行がずれで、`rex_version` と `built_at` と `api_revision` の行が `last-checked.md` に書く値。

| doctor が打つコマンド | 見る欄 | 突き合わせる先 |
| ---- | ---- | ---- |
| `version --json` | `rex_version` / `built_at`、あれば `api_revision` (CLI が対応する API の版) | `last-checked.md` の記録 |
| `api call server.status` (CLI に `api_revision` があるときだけ) | `api_revision` (server が動かしている API の版) が CLI と同じ | `exec.go` の `serverStatusResult` (`pid` は `Reachable` が読む)、`testdata/2026-10-20_…/server_status.json` |
| `block call -b block:01a12599-0000-7000-8000-000000000001 program_status` | stderr が `block target "…" not found`、exit 3 | `exec.go` の `exitTargetNotFound`、`exec_test.go` の `newFakeRex` |
| `block call -b "$REX_BLOCK" program_status` | root record (`id` が無いか空) の `state` / `app` / `owner.pid` | `pane.go` の `programStatusRecord`、`testdata/` の `program_status-*.json` |
| `block call -b "$REX_BLOCK" process` | `foreground.pid` / `argv0` / `cwd` | `pane.go` の `processResult`、`pane_test.go` の `processJSON` |
| `block ls -s "$REX_SESSION" --json` | `blocks[]` に自分の block があり `flavor` が `com.superlogical.terminal.shell` | `exec.go` の `blockLsResult` と `shellFlavor` |
| `block inspect "$REX_BLOCK" --json` | `api.methods` に `program_status` / `process` / `size` / `modes`、`api.events` に `program_status_changed` | `premise.go` の `inspectMethods` / `inspectEvents`。一覧に新しい method や event が増えていないかは人が見る (下) |
| `block call -b "$REX_BLOCK" modes` | `screen` が `alternate` (Claude Code の画面で呼んだとき) | `exec.go` の `modesResult`、`testdata/2026-10-20_…/modes.json`。pane 一覧の `rows` / `screen` は `size` (`exec.go` の `sizeResult`、`testdata/2026-10-20_…/size.json`) と合わせて `PaneGeometry` が打つ |

`--autostart=false` は全呼び出しに付くので、flag が消えると節の先頭 (`version --json`) から落ちる。
`doctor` の exit code は変わらない (rex に到達できないときだけ 1)。

## 人が見る行

streaming の `events` と、一覧の増減 (新しい method や event) は判断が要るので人が見る。

| コマンド | 見る欄 | 突き合わせる先 |
| ---- | ---- | ---- |
| `timeout 5 rex --autostart=false events --json -s "$REX_SESSION"` | `program_status_changed` の `payload.record` (`owner.pid` 付き) | #78 の本文の形 (コードが購読するようになったら `internal/rexcli`) |
| `rex --autostart=false block inspect "$REX_BLOCK" --json` の `.api.methods` / `.api.events` の key 一覧 | 前回の実測 (`testdata/<日付>/block-inspect.json`) に無い method や event | daemon の polling や画面の読み取りを置き換えられるか (アイデアとして issue へ) |

## コマンドごとの注意

- `events` は流れ続けるので coreutils の `timeout` で切る。exit 124 と stderr の `Error: interrupted` が正常。
  `program_status_changed` は record が変わったときだけ流れる。自分の block の Bash から流すと、そのコマンド自体が
  Bash ツールの実行なので自分の record は終わるまで `working` のまま動かず、timeout を伸ばしても
  `client_connected` / `client_disconnected` (daemon が tick ごとに繋ぐ分) しか来ない。そのときは `session_event` の
  封筒 (`payload.session_id` と `payload.event`) までしか確かめられない。block event の外形 (`session_event` →
  `block_event` → `name`) が #78 の形のままかは block event が来たときだけ見られ、record の中身は
  `program_status` の応答 (同じ record) で代用する。record の変化を観測したいときは、別の block で動く Claude の
  session を `-s` に指定する。
- 存在しない block の ID は `premise.go` の `probeMissingBlock` (`exec_test.go` の `blockClosed` と同じ値)。fake の rex が
  返す形 (stderr と exit 3) と実機を同じ入力で比べる。
