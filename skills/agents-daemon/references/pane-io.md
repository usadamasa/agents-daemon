# pane の読み取りと送信

上限からの再開 ([rate-limit.md](rate-limit.md)) と compact の自動化
([compact.md](compact.md)) が共通で負う制約｡

## 読み取り元

既定は `recent-unwrapped`｡herdr の 4 つのソースを viewport 16 行の pane に対して
`--lines 150` で実測した結果がこれ｡

| source | 返った行数 | 最も古い行 |
| ---- | ---- | ---- |
| `visible` | 16 | 191 |
| `recent` | 150 | 57 |
| `recent-unwrapped` | 149 | 57 |
| `detection` | 16 | 191 |

`detection` と `visible` は **pane の viewport に収まる範囲しか返さない**｡ペインを
小さく分割していると、`readLines` に何を指定しても表示行数で頭打ちになり、上限の
表示がスクロールで流れた時点で検知が黙って効かなくなる｡`recent` 系はスクロール
バックから取るため指定した行数がそのまま得られる｡

この理由で `detection` は設定できる値から外した｡`visible` は「今見えているものだけ」を
見たい場合のために残してあるが、監視に使うと同じ罠を踏む｡

`recent-unwrapped` を既定にしたのは、折り返された長い行を 1 行として扱えるため｡
上限の文言が画面幅で折り返されても、1 行の中で limit と reset の両方を拾える｡

## 送信

Escape → テキスト → Enter を**別々のキーストロークとして**送る｡まとめて送ると Claude の
ペースト検知に引っかかり、`/rate-limit-options` メニューが出ていると解除できない｡

送信文字列は入力行にエコーされる｡**検知語 (`limit` / `rate` / 過負荷を示す語 等) を
入れてはいけない｡** 自分のメッセージを自分で検知して、無限に再送する｡この制約は
`retryMessage` `compactStallMessage` `compactAutoMessage` すべてに掛かる｡

入力欄が空であることを確認してから送る｡Escape は補完メニューを閉じるだけで入力行を
クリアしないため、残った文字に送信文字列が連結される｡空判定には U+00A0 (NBSP) を
含める — 画面上の空の入力行は通常の空白ではないことがある｡

Escape を送るかは経路で分かれる｡上限からの再開では `/rate-limit-options` メニューを
閉じる必要があるため送る (`dismissMenu`、既定 true)｡compact 後の再開では送らない｡
閉じるべきメニューが無いうえ、万一ダイアログが開いていれば Escape がそれを勝手に
answer してしまう｡
