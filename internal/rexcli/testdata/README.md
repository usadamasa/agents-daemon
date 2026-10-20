# rexcli の実測 fixture

rex に打ったコマンドの応答をそのまま置く。ディレクトリ名は `<実測の日付>_rex-<版>_cc-<Claude Code の版>`。
実測に無い形 (owner の欠けた record など) はテストの Go のコードに直接書き、ここには置かない。
応答の取り方は `.claude/skills/catching-up-with-rex-changelog/references/cli-checks.md` にある。

<!-- rex-premise: preview.16 program_status 2026-10-17 の record は Claude Code の自己申告だけ -->
<!-- rex-premise: preview.16 cli 2026-10-18 の version / block ls / block inspect --json は doctor の点検 (premise.go) が読む形 -->
<!-- rex-premise: preview.16 cli 2026-10-20 の block inspect --json は api.methods の一覧の基準 (次回の増減をこれと diff する)。modes.json は preview.16 で増えた method の応答 -->
<!-- rex-premise: preview.16 cli 2026-10-20 の server_status.json は api call server.status の応答で api_revision を持つ。tailscale の object (DNS 名とアカウント) は落としてある -->

`2026-10-20_…/server_status.json` は `api call server.status` の応答から `tailscale` の object を落としてある
(公開 repo に DNS 名とアカウントを置かないため。コードは `pid` と `api_revision` しか読まない)。
