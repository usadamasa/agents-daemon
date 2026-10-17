# 実機の CLI の点検

自分の block に打つ固定のコマンドと、コードが読む欄、突き合わせる先。`internal/rexcli` が rex に打つコマンドと
同じ引数にしてある。コードが打つコマンドを変えたら、この表も同じ PR で変える。
パスは `internal/rexcli/` からの相対。実測を取り直したら `testdata/` に日付と版のディレクトリを足す
(`testdata/README.md`)。

| コマンド | コードが読む欄 | 突き合わせる先 |
| ---- | ---- | ---- |
| `rex --autostart=false block call -b "$REX_BLOCK" program_status` | `records[].id` / `state` / `app` / `owner.pid` | `pane.go` の `programStatusRecord`、`testdata/` の `program_status-*.json` |
| `rex --autostart=false block call -b "$REX_BLOCK" process` | `foreground.pid` / `argv0` / `cwd` | `pane.go` の `processResult`、`pane_test.go` の `processJSON` |
| `rex --autostart=false block ls -s "$REX_SESSION" --json` | `blocks[].block_id` / `flavor` (`com.superlogical.terminal.shell`) | `exec.go` の `blockLsResult` と `shellFlavor` |
| `rex --autostart=false block call -b block:01a12599-0000-7000-8000-000000000001 program_status; echo "exit=$?"` | stderr が `block target "…" not found`、exit 3 | `exec.go` の `exitTargetNotFound`、`exec_test.go` の `newFakeRex` |
| `rex --autostart=false api call server.status` | `pid` | `exec.go` の `serverStatusResult`、`testdata/` の `server_status.json` |
| `timeout 5 rex --autostart=false events --json -s "$REX_SESSION"` | `program_status_changed` の `payload.record` (`owner.pid` 付き) | #78 の本文の形 (コードが購読するようになったら `internal/rexcli`) |
| `rex --autostart=false block inspect "$REX_BLOCK"` | METHOD に `program_status` と `process`、EVENT に `program_status_changed` | 一覧に新しい method や event が増えていないかも見る |

## コマンドごとの注意

- `--autostart=false` が全部のコマンドで通ること自体が点検の 1 つ (flag が消えると全呼び出しが落ちる)。
- `events` は流れ続けるので coreutils の `timeout` で切る。exit 124 と stderr の `Error: interrupted` が正常。
  `program_status_changed` は record が変わったときだけ流れるので、5 秒では `client_connected` しか来ないことがある。
  来なければ `timeout 30` に伸ばして次のターンで打ち直す。それでも来なければ、外形 (`session_event` →
  `block_event` → `name`) が #78 の形のままかだけ見て、record の中身は `program_status` の応答 (同じ record) で
  代用する。
- 存在しない block の ID は `exec_test.go` の `blockClosed` と同じ値。fake の rex が返す形 (stderr と exit 3) と
  実機を同じ入力で比べる。
