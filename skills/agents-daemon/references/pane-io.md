# pane の読み取りと送信

上限からの再開 ([rate-limit.md](rate-limit.md)) と compact の自動化
([compact.md](compact.md)) が共通で負う制約｡

## 読み取り元

rex は `capture -b BLOCK --trim --unwrap` で読み､末尾 `readLines` 行に絞る｡
capture が返すのは block の可視領域だけで (52 行の block で 52 行｡実測)､`format` method の入力にも
scrollback や行数の指定が無い｡rex には scrollback を読む手段が無く､block を小さく分割していると
`readLines` に何を指定しても表示行数で頭打ちになり､上限の表示がスクロールで流れた時点で検知が
黙って効かなくなる｡上限の文言が流れる前に読めているうちは検知できるが､小さい block (10 行程度) で
limit の文と reset の行が可視領域に収まるかは測っていない｡`--unwrap` で折り返しは 1 行に戻るので､
上限の文言が画面幅で折り返されても 1 行の中で limit と reset の両方を拾える｡

## 送信

`send` / `send-key` で送る ([architecture.md](architecture.md) の対応表)｡
Escape → テキスト → Enter を**別々のキーストロークとして**送る｡まとめて送ると Claude の
ペースト検知に引っかかり､`/rate-limit-options` メニューが出ていると解除できない｡

送信文字列は入力行にエコーされる｡**検知語 (`limit` / `rate` / 過負荷を示す語 等) を
入れてはいけない｡** 自分のメッセージを自分で検知して､無限に再送する｡この制約は
`retryMessage` `compactStallMessage` `compactAutoMessage` すべてに掛かる｡

入力欄が空であることを確認してから送る｡Escape は補完メニューを閉じるだけで入力行を
クリアしないため､残った文字に送信文字列が連結される｡空判定には U+00A0 (NBSP) を
含める — 画面上の空の入力行は通常の空白ではないことがある｡

Escape を送るかは経路で分かれる｡上限からの再開では `/rate-limit-options` メニューを
閉じる必要があるため送る (`dismissMenu`､既定 true)｡compact 後の再開では送らない｡
閉じるべきメニューが無いうえ､万一ダイアログが開いていれば Escape がそれを勝手に
answer してしまう｡
