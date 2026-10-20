# 点検の記録

`catching-up-with-rex-changelog` を回すたびに 1 行足す。範囲の始まりは最後の行の changelog の版。
`rex_version` は `rex version --json` の値、changelog の版は `list_changelog` の post の版。

| 日付 | `rex_version` | `built_at` | changelog の版 | 結果 |
| ---- | ---- | ---- | ---- | ---- |
| 2026-10-17 | `0.0.1792158932-g88fd41` | 2026-10-16T13:55:32Z | preview.15 | ずれ無し。marker は全部 preview.15 (#69、#76 で確かめた直後)。`events` の形は #78 |
| 2026-10-20 | `0.0.1792433367-g93d474` | 2026-10-19T18:09:27Z | preview.16 (api_revision 4) | コードが読む欄と exit code はずれ無し。method は `tty_mode` → `input_mode` (改名、未使用) と `modes` が増えた。binary の置き場は PATH に `Versions/Current/` が先頭に加わり旧 build が残る (locate.go の marker は preview.15 のまま、#179)。`modes` は #180、`api_revision` は #181。`events` は `program_status_changed` を観測できず record は `program_status` で代用。レポートは `tmp/rex-drift/2026-10-20.md` |
