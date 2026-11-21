# リリースの前の検証

リリース PR をマージする前に、常用の環境から切り離した環境で、リリースする中身を実データの複製に当てる。
環境の作り方と、常用の環境に触れないための条件は `scripts/release-verify.sh` の冒頭が持つ。

## 手順

```sh
git fetch origin
task release-verify REF=<リリース PR の head> KEEP=1   # 建てて、複製して、機械の点検を回す。環境は残す
task release-verify:open REF=<同じ ref>                # 人が画面と IME を確かめる (status が human_required のとき)
task release-verify:approve REF=<同じ ref>             # 確かめた利用者が打つ
task release-verify:status REF=<同じ ref>              # exit 0 ならマージしてよい
task release-verify:down REF=<同じ ref>
```

- `task` 経由で打つ。Claude Code の sandbox の中から `scripts/release-verify.sh` を直に呼ぶと、daemon の書き込みを
  OS に拒ませる `sandbox-exec` を重ねられず、daemon の項目が落ちる
- `open` は窓を閉じるまで戻らないので、利用者が `!` で打つ。窓の題は `agent-tasks verify-<commit>`
- `approve` は画面を確かめた利用者だけが打つ。Claude は打たない
- 機械の点検が落ちたら環境は残る。`report/` を読んでから `down` で消す

## 記録と鮮度

記録は tree の hash を鍵に git notes (`refs/notes/release-verify`) へ置く。手元の repo にだけあり、push しない。
検証の後に commit が増えると tree が変わり、`status` は `unverified` に戻る。merge commit のように commit が違っても
tree が同じなら通る。

`status` の stdout は 1 行の JSON で、`result` は次のどれか。exit 0 は `pass` だけ。

| `result` | 意味 |
| ---- | ---- |
| `pass` | 機械の点検が通り、要るときは人の確認も済んでいる |
| `human_required` | 機械の点検は通った。前の tag から `gui/` か mod に差分があり、人の確認が残っている |
| `unverified` | この tree に通った記録が無い |

`not_covered` は、この検証が見ていない操作の一覧。通ったことを、そこまで確かめたことと読まない。

## 環境の中身

置き場は `~/tmp/agents-daemon-verify/<tree の先頭 12 桁>/`。`report/` に各コマンドの出力と daemon の log、
常用のバイナリを同じ複製に当てた出力との差分 (`*.diff`) が残る。差分は合否に入れず、挙動が変わった箇所を読む材料にする。

| 動かすもの | rex | Claude の設定ディレクトリ | state |
| ---- | ---- | ---- | ---- |
| CLI、アプリ | 存在しない socket (届かない) | 複製 (memory、skill、CLAUDE.md) | 複製 |
| daemon (`--dry-run` 固定) | 本物 (読むだけ) | 本物 (読むだけ。書き込みは `sandbox-exec` が拒む) | 複製 |

- 複製した `skills/` と `CLAUDE.md` は symlink のままで、原本を指す。隔離の env で `dream adopt` と `dream forget` を
  打たない (原本に届く)
- アプリの Spawn、Tell、メモを渡す操作は rex に届かず失敗する。この環境では確かめられない
- daemon の env には `gh` が無く、外部の参照の状態は複製した `refs/` のまま
