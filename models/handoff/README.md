# ボードの受け渡しのモデル (TLA+)

パーキングのメモをセッションの間で渡す手順と、タスクの記録を hook が書く手順を、今の実装のまま TLA+ に写したもの。
複数のプロセス (各セッションの mod、hook、daemon) がロックの無いファイル越しに 1 つの状態を書き換えるので、
個々の関数ではなく手順の交錯を TLC に列挙させる (#242、#243)。

| モジュール | 写しているもの |
| ---- | ---- |
| `Handoff.tla` | メモ 1 つを mod が渡す (peer / self / spawn)、捨てる、終了したセッションのタスクを adopt する。daemon の生き死に |
| `TaskLog.tla` | TaskCreate と TaskList の PostToolUse hook がタスクの記録を書く・同期する |

どのステップが実装のどこに当たるかは `.tla` のコメントが持つ (ファイル:行)。

## 回し方

```sh
task model-check          # 全 cfg を TLC で回し、期待 (反例なし / 反例あり) と突き合わせる
```

TLC は devShell の `tlaplus` (`tlc`)。CI では回さない。`scripts/model-check.sh` が `./tmp/tlc/` に
標準モジュールの展開先と探索の状態を置き、cfg ごとの log と反例の trace もそこに残す。

| cfg | 不変条件 | 今の設計 |
| ---- | ---- | ---- |
| `Handoff.cfg` | `TypeOK`、`AdoptLeavesOne` (adopt でメモかタスクの一方は残る) | 成り立つ |
| `Handoff.AtMostOneDelivery.cfg` | 1 つのメモは高々 1 つの宛先に渡る | 破れる。2 つの mod が同じ open のメモを読み、両方が送る |
| `Handoff.LiveTaskKept.cfg` | 生きているセッションのタスクの記録は adopt で消えない | 破れる。daemon が止まると pane が空に見え、adopt が通る |
| `Handoff.AtMostOneSession.cfg` | 1 つのメモから起きるセッションは高々 1 つ | 破れる。2 つの mod がそれぞれ spawn する。1 つの mod でも、前景待ちが切れた後にもう一度 spawn すると起きる |
| `TaskLog.cfg` | TaskCreate の記録は失われない | 破れる。TaskList が先に実行され、その hook が TaskCreate の hook の後に動くと記録を消す |

「破れる」は今の設計の写しとして正しい結果で、`model-check.sh` は違反を期待する。設計を直すとき (#244) は、
直しをモデルに写して `violated` を `holds` に変え、反例が出なくなることを確かめてから実装する。

## 前提

- ファイルの書き込みは `statefile.WriteJSONAtomic` なので、1 回の書き込みを原子的な 1 ステップにしている。
- 送信と spawn の成否、daemon の生き死には非決定。各 mod が同じメモを渡そうとする回数は `MaxAttempts` まで。
- `Handoff.tla` のセッションは生きているものだけを置く。終了したセッションのタスクの adopt は正しい操作で、
  モデルの対象にしていない。
- hook は PostToolUse で、同じターンの並列の tool の hook は順序が決まらないとしている。
