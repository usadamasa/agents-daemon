# rexcli の実測 fixture

rex に打ったコマンドの応答をそのまま置く。ディレクトリ名は `<実測の日付>_rex-<版>_cc-<Claude Code の版>`。
rex の `<版>` は Mac の changelog の版 (`preview.19`) で、下の `rex-premise` marker の `<version>` と同じ。
CLI の hash は書かない。
実測に無い形 (owner の欠けた record など) はテストの Go のコードに直接書き、ここには置かない。
応答の取り方は `.claude/skills/catching-up-with-rex-changelog/references/cli-checks.md` にある。

<!-- rex-premise: preview.21 program_status 2026-10-17 の record は Claude Code の自己申告だけ -->
<!-- rex-premise: preview.21 cli 2026-10-18 の version / block ls / block inspect --json は doctor の点検 (premise) が読む形 -->
<!-- rex-premise: preview.21 cli 2026-10-20 の modes.json は preview.16 で増えた method の応答、size.json は PaneGeometry が読む rows / columns -->
<!-- rex-premise: preview.21 cli 2026-10-20 の server_status.json は api call server.status の応答で api_revision を持つ。tailscale の object (DNS 名とアカウント) は落としてある -->
<!-- rex-premise: preview.21 cli 2026-10-24 の block inspect --json は api.methods の一覧の基準 (次回の増減をこれと diff する)。api_revision 6 で blob_read が増え、format と selection_copied の schema が変わった -->
<!-- rex-premise: preview.21 cli 2026-10-25 の api-list.json は api list --json の応答で、server の method 一覧の基準 (次回の増減をこれと diff する)。api_revision 7 で block の api は 2026-10-24 と同じ -->
<!-- rex-premise: preview.21 cli 2026-10-27 の events.jsonl は events --all --json の 1 行 1 event。自分の session と使い捨ての block の分だけを抜き、process_changed の ancestors は落としてある -->
<!-- rex-premise: preview.21 cli 2026-10-27 の events-session-new.jsonl は session new の直後の行。最初の block には block_created が来ず、session_created の後にいきなりその block の event が流れる -->

`blockstate` のテストもこの testdata を読む (`../testdata`)。実測の応答を 1 か所に置くため。

`2026-10-20_…/server_status.json` は `api call server.status` の応答から `tailscale` の object を落としてある。
公開 repo に DNS 名とアカウントを置かないためで、コードは `pid` と `api_revision` しか読まない。
