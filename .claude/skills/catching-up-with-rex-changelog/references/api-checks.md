# 実機の control API の点検

自分の block に打つ固定の呼び出しと、コードが読む欄、突き合わせる先。`rex` が rex に打つ呼び出しと
同じ method と引数にしてある。コードが打つ呼び出しを変えたら、この表も同じ PR で変える。
パスは `rex` パッケージのディレクトリからの相対。実測を取り直したら `testdata/` に日付と版のディレクトリを足す
(`testdata/README.md`)。

## doctor が見る行

`go run ./cmd/agents-daemon doctor` の「rex の前提」の節が、自分の block に打って
欄の有無と失敗の形を名指しで出す (`rex/premise`)。人が打ち直す必要は無い。
節の `⚠` の行がずれで、`rex_version` と `built_at` と `api_revision` の行が `last-checked.md` に書く値。

| doctor が打つ呼び出し | 見る欄 | 突き合わせる先 |
| ---- | ---- | ---- |
| `server.version` | `rex_version` / `built_at`、`api_revision` と `api_min_revision` (server が対応する revision の幅) に daemon の revision が入るか | `last-checked.md` の記録、`control` の `APIRevision`、`testdata/2026-11-18_…/server-version.json` |
| なし (`wrap.Locate`) | spawn の包みが叩く rex のパス。`Versions/` のどの build に解決したか | `wrap/locate.go` |
| `target.resolve` (存在しない block)、自分の session での `program_status` (存在しない block)、`session.list_blocks` (存在しない session) | 順に status `not_found`、error の code `not_found`、`session_not_found` | `api` の `IsNotFound`、`control` の `IsNotFound` |
| `com.superlogical.terminal.program_status` | root record (`id` が無いか空) の `state` / `app` / `owner.pid` | `wire` の `Record` と `RecordFields`、`testdata/` の `program_status-*.json` |
| `com.superlogical.terminal.process` | `foreground.pid` / `argv0` / `cwd` | `wire` の `Foreground` と `ForegroundFields`、`fixtures_test.go` の `processJSON` |
| `session.list_blocks` (自分の session) | `blocks[]` に自分の block があり `flavor` が `com.superlogical.terminal.shell` | `wire` の `BlockList` と `ShellFlavor` |
| `session.describe_block` | `api.methods` に `program_status` / `process` / `size` / `modes`、`api.events` に `program_status_changed` / `program_status_removed` / `process_changed` / `child_exited` (購読が読む block の event) | `premise` の `describeMethods` と `wire` の `BlockEvents`。一覧に新しい method や event が増えていないかは人が見る (下) |
| `com.superlogical.terminal.modes` | `screen` が `alternate` (Claude Code の画面で呼んだとき) | `wire` の `Modes`、`testdata/2026-10-20_…/modes.json`。pane 一覧の `rows` / `screen` は `size` (`testdata/2026-10-20_…/size.json`) と合わせて `PaneGeometry` が打つ |

自分の session は、自分の block から `target.resolve` で引いてから打つ。
`doctor` の exit code は変わらない (rex に到達できないときだけ 1)。

## 人が見る行

購読の event と、一覧の増減 (新しい method や event、revision の変更) は判断が要るので人が見る。
socket は `REX_SERVER` の `unix://` の後ろ (percent-encoded) で、既定は `~/Library/Application Support/rex/server.sock`。

| 打つもの | 見る欄 | 突き合わせる先 |
| ---- | ---- | ---- |
| `timeout 10 rex --autostart=false events --all --json` | daemon の購読 (`session.attach_all` の `event_scope: all`) と同じ event の payload。`block_event` の `name` と payload (`program_status_changed` の `id` / `record`、`program_status_removed` の `ids`、`process_changed` の `foreground`、`state=idle` で終わった block の `child_exited` の後に record が残るか)、`block_created` / `block_closed` の `block_id`、`session_destroyed` の `session_id` | `blockstate` の `cache.go`、`wire` の `Event`、`testdata/2026-10-27_…/events.jsonl` |
| `curl -s --unix-socket <socket> http://localhost/api/help` の `.methods[].name` と、daemon が打つ method の `input_schema` / `output_schema` | 前回の実測 (`testdata/<日付>/api-list.json`。`rex api list --json` と同じ応答) に無い method、daemon が打つ method の schema の変化 | daemon の polling や画面の読み取りを置き換えられるか (アイデアとして issue へ)。schema の変化はずれ |
| `session.describe_block` の `.api.methods` / `.api.events` の key 一覧 (CLI なら `rex --autostart=false block inspect <block> --json`。`<block>` は SKILL.md の手順 4 で取った自分の block) | 前回の実測 (`testdata/<日付>/block-inspect.json`) に無い method や event | 同上 |
| `curl -s --unix-socket <socket> http://localhost/llms.txt` の「API Revisions」と「Combining Results With Events」の節 | `server.version` の `api_revision` が前回 (`last-checked.md`) から上がったとき、daemon の revision (14) の応答の形が変わらないか、event と応答の `seq` の決めごとが変わらないか | `control` の `APIRevision`、`watch` の `once` (購読を張った後に全列挙し、溜まった event を全部当て直す論法) |

## 打ち方の注意

- `events` は流れ続けるので coreutils の `timeout` で切る。exit 124 と stderr の `Error: interrupted` が正常。
  CLI の `events --all --json` は control 接続の event の payload を 1 行ずつ出すので、daemon の購読と同じ形を見られる。
  `program_status_changed` は record が変わったときだけ流れ、自分の block の record は Bash ツールの実行中は
  `working` のまま動かない。block の出来事を一通り見るには、流している間に
  `rex run --focus=false --keep-open --shell=none -- <OSC 7501 を出して数秒で終わる script>` で使い捨ての block を開き、
  終わったら `block close` で閉じる (`block_created` → `program_status_changed` → `program_status_removed` →
  `child_exited` → `block_closed` が流れる)。前景の入れ替わり (`process_changed`) は対話の shell の block に
  `send` でコマンドを打つと見られる。
- 存在しない block と session の ID は `premise` の `ProbeMissingBlock` と `ProbeMissingSession`。
- `/api/help` は 300 KB ほどあるので `./tmp` に保存してから `jq` で読む。
