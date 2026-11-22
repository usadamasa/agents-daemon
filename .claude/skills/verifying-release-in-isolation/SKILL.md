---
name: verifying-release-in-isolation
description: agents-daemon のリリース PR をマージする前に、常用の daemon と plugin から切り離した環境で、リリースする中身を実データの複製に当てて確かめるときに使う。リリース PR の検証が通っているかを見るとき、検証の環境が残っているとき、検証の点検が落ちたときにも引く。
---

# verifying-release-in-isolation

常用の環境から切り離した環境で、リリースする中身を実データの複製に当てる。道具は `tools/release-verify`。

回すかどうかは、前の tag からの差分のリスクで決める。実データや実機の環境でしか出ない不具合がありうる差分なら回す。

## 手順

```sh
git fetch origin
task release-verify REF=<リリース PR の head> KEEP=1   # 建てて、複製して、機械の点検を回す。KEEP=1 は通った後も環境を残す
task release-verify:status REF=<同じ ref>              # exit 0 ならマージしてよい。human_required なら次の 2 つを打つ
task release-verify:open REF=<同じ ref>                # 人が確かめる
task release-verify:approve REF=<同じ ref>             # 人の確認を記録する。この後に status をもう一度見る
task release-verify:down REF=<同じ ref>
```

- 検証を回すセッションの rex のタブに、作業を表す名前を付ける
- `open` はアプリのウィンドウを閉じるまで戻らず、Claude の Bash は待ち続けるので、利用者が `!` で打つ
- `approve` は確かめた利用者だけが打つ。Claude は打たない
- 機械の点検が fail になると、`KEEP` を付けていなくても環境は残る。`report/` を読んでから `down` で消す
- main に commit が入ると tagpr がリリース PR の head を作り直すので、検証はやり直しになる

## 記録と鮮度

記録は tree の hash を鍵に、環境の置き場の `records/<tree>.json` へ置く。
検証の後に commit が増えると tree が変わり、`status` は `unverified` に戻る。merge commit のように commit が違っても
tree が同じなら通る。

`status` の stdout は 1 行の JSON で、`result` は次のどれか。exit 0 は `pass` だけ。

| `result` | 意味 |
| ---- | ---- |
| `pass` | 機械の点検が通り、要るときは人の確認も済んでいる |
| `human_required` | 機械の点検は通った。前の tag から `gui/` か mod に差分があり、人の確認が残っている |
| `unverified` | この tree に通った記録が無い |

同じ JSON の `not_covered` は、この検証が見ていない操作の一覧。`pass` は、その操作を確かめたことを意味しない。

## 環境の中身

置き場は `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/release-verify/<tree の先頭 12 桁>/`。`report/` に各コマンドの出力と
daemon の log、常用のバイナリを同じ複製に当てた出力との差分 (`*.diff`) が残る。差分は合否に入れず、挙動が変わった箇所を読む材料にする。

| 動かすもの | rex | Claude の設定ディレクトリ | state |
| ---- | ---- | ---- | ---- |
| CLI、アプリ | 存在しない socket (届かない) | 複製 (memory、skill、CLAUDE.md。symlink は原本を指す) | 複製 |
| daemon (`--dry-run` 固定) | 本物 (読むだけ) | 本物 (読むだけ。書き込みは `sandbox-exec` が拒む) | 複製 |

- 複製した `skills/` と `CLAUDE.md` は symlink のままで、原本を指す。この環境で `dream adopt` と `dream forget` を
  打たない (原本に届く)
- アプリの Spawn、Tell、メモを渡す操作は rex に届かず失敗する。この環境では確かめられない
- daemon に渡す PATH には `gh` が無く、外部の参照の状態は複製した `refs/` のまま