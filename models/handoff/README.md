# ボードの受け渡しのモデル (TLA+)

パーキングのメモをセッションの間で渡す手順と、タスクの記録を hook が書く手順を TLA+ に写したもの。
複数のプロセス (各セッションの mod、hook、daemon) がロックの無いファイル越しに 1 つの状態を書き換えるので、
個々の関数ではなく手順の交錯を TLC に列挙させる (#242)。設計を変えるときは、先にここへ写して反例が出ないことを
見てから実装する。

| モジュール | 写しているもの |
| ---- | ---- |
| `Handoff.tla` | メモ 1 つを mod が取って渡す (peer / self / spawn)、戻す、捨てる。セッションの終了と daemon の見え方、終了したセッションのタスクを adopt する |
| `TaskLog.tla` | TaskCreate と TaskList の PostToolUse hook がタスクの記録を書く・同期する |

どのステップが実装のどこに当たるかは `.tla` のコメントが持つ (ファイルと関数)。

## 回し方

```sh
task model-check          # 全 cfg を TLC で回し、期待 (反例なし / 反例あり) と突き合わせる
```

TLC は devShell の `tlaplus` (`tlc`)。CI では回さない。`scripts/model-check.sh` が `./tmp/tlc/` に
標準モジュールの展開先と探索の状態を置き、cfg ごとの log もそこに残す。

| cfg | 不変条件 | 成り立つ理由 |
| ---- | ---- | ---- |
| `Handoff.cfg` | `AtMostOneDelivery`: 1 つのメモは高々 1 つの宛先に渡る | 送る前に `park set --status handed --from open` で取る。lock の中で読んでから書くので取れるのは 1 つの mod だけ |
| | `AtMostOneSession`: 1 つのメモから起きるセッションは高々 1 つ | 取ってから spawn する。claude が前景にならなかった (起きたか分からない) メモは戻さず handed のまま人に知らせる |
| | `LiveTaskKept`: 生きているセッションのタスクの記録は adopt で消えない | Adopt は、今の daemon がタスクの記録より後に書いた status.json に pane が無いときだけ通す |
| | `AdoptLeavesOne`: adopt でメモかタスクの一方は残る | メモを書いてから記録を消す |
| | `HandedWhenDelivered`: 届いたメモは handed のまま | Discard も `--from open` で、取られたメモを上書きしない |
| `TaskLog.cfg` | `RecordsComplete`: TaskCreate の記録は失われない | TaskList の sync は、一覧の最大の id より大きい id の記録 (一覧の後に作られた) を消さない |

どの cfg も `holds` を期待し、反例が出れば `model-check.sh` は exit 1 にする。

## 前提

- ファイルの書き込みは `statefile.WriteJSONAtomic` なので、1 回の書き込みを原子的な 1 ステップにしている。
- 送信と spawn の成否、`park set` の成否、セッションの終了、daemon の生き死には非決定。各 mod が同じメモを渡そうとする
  回数は `MaxAttempts` まで。
- `park set` の戻しが失敗したメモは handed のまま残る (人が `park set <id> --status open` で戻す)。これは性質を破らない
  (届いていないメモが handed なだけ) ので、モデルでも失敗を許している。
- 始まりの状態では daemon がそのセッションを生きていると見ている。タスクの記録より古い status.json は Adopt が受け付けない
  ので、「記録はあるのに daemon がまだそのセッションを見ていない」状態は始まりに置かない。
- hook は PostToolUse で、同じターンの並列の tool の hook は順序が決まらないとしている。Claude Code のタスクの id は
  セッションの中で 1 から順に増える整数としている (`internal/tasklog` の cc-premise)。
- housekeeping (mtime による削除) は置いていない。1 回の受け渡しの間に上の不変条件と絡まないため。
- 不変条件は `Mods = {m1}` でも回る。取ってから送る形は、mod 同士の取り合いだけでなく同じ mod の押し直しも閉じる。
