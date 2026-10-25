# pane の読み取りと送信

上限からの再開 ([rate-limit.md](rate-limit.md)) が負う制約。

## 読み取り元

rex は `capture -b BLOCK --trim --unwrap` で読み、末尾 `readLines` 行に絞る。判定 (`detect.Classify` と
起床時刻の読み取り) は更に末尾 `detectionTailLines` 行だけを見るので、`readLines` がそれ以上あれば判定は変わらない。

<!-- rex-premise: preview.20 cli capture は alternate screen では可視の行だけ、通常の画面では scrollback も返す -->
<!-- cc-premise: 2.1.302 limit-screen Claude Code は alternate screen (?1049h) で描き、上限の文は画面の下から 10 行目に出る -->

Claude Code は alternate screen で描くので、Claude の block の capture は可視の行数で頭打ちになる
(24 行の block で 24 行。alternate screen でない shell の block は scrollback ごと返り、500 行出すと 512 行)。
`format` method の入力にも scrollback や行数の指定が無く、block を小さく分割していると `readLines` に
何を指定しても表示行数で頭打ちになる。上限の表示が画面から押し出された時点で検知は黙って効かなくなる。

実機の上限到達画面 (`internal/detect/real_sample_test.go`、statusline 3 行) を alternate screen に出し、
`block call resize` で高さを変えて `inspect --pane` で測った下限は次のとおり。

| block の行数 | 判定 | 画面の先頭 |
| ---- | ---- | ---- |
| 24 | limit | 上限の文の 3 行上から全部 |
| 11 | limit | 上限の文 (下から 10 行目) |
| 10 | none | `/upgrade` の行。上限の文が画面の外へ出る |

statusline が 1 行少なければ下限も 1 行下がる。`/rate-limit-options` のメニューが出た状態と Claude Code 自身の
再描画 (縮めた後の描き直し) は測っておらず、メニューの分だけ下限は上がりうる (#39)。測るのは次に上限を踏んだときで、
daemon が踏んだ瞬間の画面と block の高さを `limit-events/` に残す ([verification.md](verification.md)「次に上限を踏んだとき」)。
下限は `detect.MinDetectableRows` (11) にあり、`doctor` の pane 一覧が block の高さ (`size` の `rows`) と比べて ⚠ を出す。
`--unwrap` で端末の折り返しは 1 行に戻るが、Claude Code が自分で改行した行は戻らない。

### block が alternate screen にいるかは `modes` で見える

<!-- rex-premise: preview.20 cli block call modes の screen は primary か alternate。idle の terminal を起こさない (changelog。idle の定義は docs に無い) -->
<!-- cc-premise: 2.1.304 limit-screen 起動直後の trust dialog は primary screen、本体の画面は alternate (?1049)。/exit で primary に戻す -->

rex preview.16 の `block call modes` は `screen` (`primary` / `alternate`) を返す。`doctor` の「rex CLI の前提」が
自分の block で `alternate` を点検し、pane 一覧に pane ごとの値を出す。使い捨ての block で Claude Code を起こして
測った値は次のとおり (2026-10-20、rex preview.16、Claude Code 2.1.304)。

| Claude Code の状況 | `screen` | 非既定の mode | `program_status` の record | `process` の foreground |
| ---- | ---- | ---- | ---- | ---- |
| 起動直後 (trust dialog) | `primary` | 無し | 無し | claude |
| 本体の画面 | `alternate` | 1049、2004、mouse 系 | `idle` | claude |
| `/exit` の直後 | `primary` | 無し (全部戻る) | 無し (`clear` で消える) | null |
| SIGKILL の直後 | `alternate` のまま | 残ったまま | `idle` が残る | null |

SIGKILL では画面も record も残るので、`screen` で Claude Code の終了は見分けられない。終了の判定は
`process` の foreground の pid と record の owner の突き合わせのまま ([program-status.md](program-status.md))。
`screen` は「capture が可視の行だけを返す前提に乗っているか」を見るためだけに使う。

## 送信

`send` / `send-key` で送る ([multiplexer.md](multiplexer.md) の対応表)。
Escape → テキスト → Enter を**別々のキーストロークとして**送る。まとめて送ると Claude の
ペースト検知に引っかかり、`/rate-limit-options` メニューが出ていると解除できない。

送信文字列は入力行にエコーされる。**検知語 (`limit` / `rate` / 過負荷を示す語 等) を
入れてはいけない。** 自分のメッセージを自分で検知して、無限に再送する。この制約は
`retryMessage` に掛かる。

上限からの再開では `/rate-limit-options` メニューを閉じる必要があるため、先に Escape を送る
(`dismissMenu`、既定 true)。Escape は補完メニューを閉じるだけで入力行をクリアしないので、
残った文字があれば送信文字列が連結される。画面上の空の入力行は通常の空白ではなく U+00A0 (NBSP)
のことがある。
