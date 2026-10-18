# rexcli の実測 fixture

rex に打ったコマンドの応答をそのまま置く。ディレクトリ名は `<実測の日付>_rex-<版>_cc-<Claude Code の版>`。
実測に無い形 (owner の欠けた record など) はテストの Go のコードに直接書き、ここには置かない。
応答の取り方は `.claude/skills/catching-up-with-rex-changelog/references/cli-checks.md` にある。

<!-- rex-premise: preview.15 program_status 2026-10-17 の record は Claude Code の自己申告だけ -->
<!-- rex-premise: preview.15 cli 2026-10-18 の version / block ls / block inspect --json は doctor の点検 (premise.go) が読む形 -->
