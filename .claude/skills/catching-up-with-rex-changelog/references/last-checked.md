# 点検の記録

`catching-up-with-rex-changelog` を回すたびに 1 行足す。次回に読む changelog の範囲は、最後の行の Mac の版の次から。
`rex_version`、`built_at`、`api_revision` は `rex version --json` の値 (`api_revision` は CLI が対応する API の版、
無い版は `-`)。Mac は `(Mac)` の投稿の `preview.N` で、marker の `<version>` と同じ値。
CLI は `(CLI)` の投稿の hash で、`rex version --json` の `commit` の先頭 7 桁と同じ。CLI の投稿が無い版は `-`。
`api_revision` が前回から上がっていれば、changelog に載らない record や応答の形の変更があり得る節目なので、
手順 4 の実機の点検を重く見る。

| 日付 | `rex_version` | `built_at` | `api_revision` | Mac | CLI | 結果 |
| ---- | ---- | ---- | ---- | ---- | ---- | ---- |
| 2026-10-17 | `0.0.1792158932-g88fd41` | 2026-10-16T13:55:32Z | - | preview.15 | - | ずれ無し。marker は全部 preview.15 (#69、#76 で確かめた直後)。`events` の形は #78 |
| 2026-10-20 | `0.0.1792433367-g93d474` | 2026-10-19T18:09:27Z | 4 | preview.16 | - | コードが読む欄と exit code はずれ無し。method は `tty_mode` → `input_mode` (改名、未使用) と `modes` が増えた。binary の置き場は PATH に `Versions/Current/` が先頭に加わり旧 build が残る (locate.go の marker は preview.15 のまま、#179)。`modes` は #180、`api_revision` は #181。`events` は `program_status_changed` を観測できず record は `program_status` で代用。レポートは `tmp/rex-drift/2026-10-20.md` |
| 2026-10-21 | `0.0.1792519967-gd05acf` | 2026-10-20T18:12:47Z | 4 | preview.17 | - | ずれ無し。changelog の CLI の節は `server start --env` だけで前提に当たらず、method / event の一覧は 2026-10-20 の fixture と同じ (fixture は足していない)。`Versions/` は `1327` と `Current` だけになり旧 build は消えた (#179 へコメント、locate.go の marker は preview.15 のまま)。`events` で `process_changed` / `size_changed` の block event を観測、`program_status_changed` は前回同様 `program_status` で代用。レポートは `tmp/rex-drift/2026-10-21.md` |
| 2026-10-24 | `0.0.1792697926-gbd4573` | 2026-10-22T19:38:46Z | 6 | preview.19 | bd45734 | ずれ無し。changelog は platform ごとの投稿に分かれた (#224)。api_revision 4 → 6 で method `blob_read` が増え、`format` と `selection_copied` の schema が変わった (コードは使わない)。event の一覧は同じ。record と process の欄は 2026-10-18 と同じ。`Versions/` は `1381` と `Current` と `.staging-…` (#179 は open のまま、locate.go の marker は preview.15)。Kitty graphics は capture の text に影響しない。preview.20 / CLI 623b2d1 は build より新しいので次回。レポートは `tmp/rex-drift/2026-10-24.md` |
| 2026-10-25 | `0.0.1792787366-g623b2d` | 2026-10-23T20:29:26Z | 7 | preview.20 | 623b2d1 | ずれ無し。changelog は session の命名の既定 off、port 7297、Tailscale の回復、MTU の警告で前提に当たらない。api_revision 6 → 7 だが block の api は 2026-10-24 の fixture と同じ。server の method 一覧は基準が無かったので `testdata/2026-10-25_…/api-list.json` を足した。`events` は前回同様 envelope だけ。`Versions/` は `1408` と `Current` と `.staging-…`、PATH は Current が先。レポートは `tmp/rex-drift/2026-10-25.md` |
| 2026-10-27 | `0.0.1793047680-g2db882` | 2026-10-26T20:48:00Z | 7 | preview.21 | 2db8821 | ずれ無し。CLI の節は terminfo の ssh guard と Linux の socket の置き場で前提に当たらない。block と server の method / event の key は前回の fixture と同じで、schema の差は `program_status_removed.reason` に `foreground` が増えたこと (Mac の「Ctrl+C で止めた program が Activity で finished になる」) と client の platform に `freebsd` が増えたことだけ。使い捨ての block で `state=working` を出して終了すると record は消え、done を補う record は来ない (pane.go の前提は保たれる)。`foreground` の購読は #38 へコメント。`Versions/` は `1497` と `Current` と `.staging-…`、PATH は Current が先。レポートは `tmp/rex-drift/2026-10-27.md` |
