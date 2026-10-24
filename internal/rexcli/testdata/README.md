# rexcli の実測 fixture

rex に打ったコマンドの応答をそのまま置く。ディレクトリ名は `<実測の日付>_rex-<版>_cc-<Claude Code の版>` で、
rex の `<版>` は Mac の changelog の版 (`preview.19`。marker の `<version>` と同じで、CLI の hash は書かない)。
実測に無い形 (owner の欠けた record など) はテストの Go のコードに直接書き、ここには置かない。
応答の取り方は `.claude/skills/catching-up-with-rex-changelog/references/cli-checks.md` にある。

<!-- rex-premise: preview.19 program_status 2026-10-17 の record は Claude Code の自己申告だけ -->
<!-- rex-premise: preview.19 cli 2026-10-18 の version / block ls / block inspect --json は doctor の点検 (premise.go) が読む形 -->
<!-- rex-premise: preview.19 cli 2026-10-20 の modes.json は preview.16 で増えた method の応答、size.json は PaneGeometry が読む rows / columns -->
<!-- rex-premise: preview.19 cli 2026-10-20 の server_status.json は api call server.status の応答で api_revision を持つ。tailscale の object (DNS 名とアカウント) は落としてある -->
<!-- rex-premise: preview.19 cli 2026-10-24 の block inspect --json は api.methods の一覧の基準 (次回の増減をこれと diff する)。api_revision 6 で blob_read が増え、format と selection_copied の schema が変わった -->

`2026-10-20_…/server_status.json` は `api call server.status` の応答から `tailscale` の object を落としてある
(公開 repo に DNS 名とアカウントを置かないため。コードは `pid` と `api_revision` しか読まない)。
