# メモの受け渡しのモデル (TLA+)

パーキングのメモをセッションの間で渡す手順と、タスクの記録を hook が書く手順を TLA+ に写したもの。
複数のプロセス (各セッションの mod、hook、daemon) がロックの無いファイル越しに 1 つの状態を書き換えるので、
個々の関数ではなく手順の交錯を TLC に列挙させる (#242)。設計を変えるときは、先にここへ写して反例が出ないことを
見てから実装する。

| モジュール | 写しているもの |
| ---- | ---- |
| `Handoff.tla` | メモ 1 つを mod が取って渡す (peer / self / spawn)、戻す、捨てる。セッションの終了と daemon の見え方、終了したセッションのタスクを adopt する |
| `TaskLog.tla` | TaskCreate と TaskList の PostToolUse hook がタスクの記録を書く・同期する |
| `FileNotice.tla` | ファイルを触った tool の結果に、そのファイルに関係するメモを model 向けに添える。並列の tool の hook、呼び出しごとに `park list` で読んだ写しが update までに古くなること、compact |
| `EdgeBind.tla` | daemon が辺の `session:<名前>` の端点をセッション台帳のセッションに結びつける。CLI の `edge add` / `edge rm` と同じ辺のファイルを交錯して書く |

どのステップが実装のどこに当たるかは `.tla` のコメントが持つ (ファイルと関数)。

## 回し方

```sh
task model-check          # 全 cfg を TLC で回し、期待 (反例なし / 反例あり) と突き合わせる
```

TLC は devShell の `tlaplus` (`tlc`)。CI では回さない。`scripts/model-check.sh` が `./tmp/tlc/` に
標準モジュールの展開先と探索の状態を置き、cfg ごとの log もそこに残す。

| cfg | 不変条件 | 成り立つ理由 |
| ---- | ---- | ---- |
| `Handoff.cfg` | `AtMostOneDelivery`: 1 つのメモは高々 1 つの宛先に渡る | 送る前に `park set` (status handed、from open) で取る。lock の中で読んでから書くので取れるのは 1 つの mod だけ |
| | `AtMostOneSession`: 1 つのメモから起きるセッションは高々 1 つ | 取ってから spawn する。claude が前景にならなかった (起きたか分からない) メモは戻さず handed のまま人に知らせる |
| | `LiveTaskKept`: 生きているセッションのタスクの記録は adopt で消えない | Adopt は、今の daemon がタスクの記録より後に書いた status.json に pane が無いときだけ通す |
| | `AdoptLeavesOne`: adopt でメモかタスクの一方は残る | メモを書いてから記録を消す |
| | `HandedWhenDelivered`: 届いたメモは handed のまま | Discard も from open で、取られたメモを上書きしない |
| | `HandedHasDestination`: handed のメモは宛先 (`handed_to`) を持つ | 取るときに宛先を同じ `park set` (to_session / to_repo) で書く |
| | `OnlyHandedHasDestination`: handed でないメモは宛先を持たない | 戻す・捨てる `park set` は宛先を空にする |
| | `DeliveredWhereRecorded`: 届いたメモの宛先は届いた先と同じ | mod は取ったときの宛先へ送り、spawn は起こす先の repo のまま書き直さない |
| `HandoffSplit.cfg` | `HandedHasDestination` が破れる (`violated` を期待) | 取るのと宛先を書くのを別の `park set` にすると、宛先の無い handed が見える。1 回の `park set` にした根拠 |
| `TaskLog.cfg` | `RecordsComplete`: TaskCreate の記録は失われない | TaskList の sync は、一覧の最大の id より大きい id の記録 (一覧の後に作られた) を消さない |
| `FileNotice.cfg` | `AtMostOnce`: 1 つのメモを添えるのは 1 つの context の中で高々 1 回 | 添えるメモを選ぶのと told に足すのを 1 回の `update` の中で行う。読んだ一覧が古くても、選ぶときに told を見る |
| | `NoneMissed`: 触ったファイルに関係するメモは、hook が済んだ時点で伝わっている | 読んだ一覧にあって told に無いものは全部添える。読んだ後に置かれたメモは次に触ったときに伝える |
| | `Informed`: compact の後に触ったファイルのメモを model が知っている | compact で told も空にし、compact の後に触ったときに伝え直す |
| `FileNoticeRace.cfg` | `AtMostOnce` が破れる (`violated` を期待) | told を読んでから別のステップで書くと、並列の 2 つの Read が同じメモを添える。選ぶのと足すのを 1 回にした根拠 |
| `FileNoticeCompact.cfg` | `Informed` が破れる (`violated` を期待) | compact の後も told を残すと、compact の前に伝えたメモを伝え直さない。添えたメモが要約に残るかは決まらないので、残らない側に倒している。compact で told を空にした根拠 |
| `EdgeBind.cfg` | `BindOnce`: 結びついた端点は、辺が消されるまで名前や別のセッションに戻らない | `Add`、`Remove`、`Bind` が `edges/.lock` の中で読んでから書く。`edge add` は辺があれば端点を残す |
| | `NoResurrect`: `edge rm` で消した辺を結びつけの書き込みが作り直さない (辺を無い状態から作るのは `edge add` だけ) | `Bind` はロックの中で、辺があるときだけ書き直す |
| `EdgeBindRaceAdd.cfg` | `BindOnce` が破れる (`violated` を期待) | ロック無しで `edge add` が読んでから書くと、間に結びついた端点を読んだ値 (名前) に戻す |
| `EdgeBindRaceRemove.cfg` | `NoResurrect` が破れる (`violated` を期待) | ロック無しで結びつけが読んでから書くと、間に `edge rm` で消した辺を作り直す。ロックを取った根拠 |

`HandoffSplit.cfg`、`FileNoticeRace.cfg`、`FileNoticeCompact.cfg`、`EdgeBindRaceAdd.cfg`、`EdgeBindRaceRemove.cfg` 以外の cfg は
`holds` を期待する。期待と違えば `model-check.sh` は exit 1 にする。

同じ不変条件を、実物の Store を goroutine で交錯させる乱択テストが実装に対して持つ (`task test` で回る)。
モデルは設計を変えるときに先に反例を探す場所で、実装の正しさはテストが持つ。

| 不変条件 | テスト |
| ---- | ---- |
| `AtMostOneDelivery` / `AtMostOneSession` / `HandedWhenDelivered` / `HandedHasDestination` / `OnlyHandedHasDestination` / `DeliveredWhereRecorded` と Claim の排他 | `parking/invariants_test.go` |
| `LiveTaskKept` / `AdoptLeavesOne` (実物の `daemonstate.SessionExited` で daemon の pid と status.json を読む) | `parking/adopt_invariants_test.go` |
| `RecordsComplete` | `tasklog/invariants_test.go` |
| `BindOnce` / `NoResurrect` と、書いたときの参照の組・今の参照の組ごとに辺が 1 本 | `edge/bind_invariants_test.go` |

mod の側 (取る → 送る → 届かなければ戻す、spawn で前景にならなければ戻さない、Discard は from open) は
`hooks/deps.test.tsx` が `park` と `spawn` の呼び出しの列で固定する。`AtMostOnce` / `NoneMissed` / `Informed` は
`hooks/notice.test.tsx` が並列の `tool.call` と `session.compact` で固定する。

## 前提

- ファイルの書き込みは `statefile.WriteJSONAtomic` なので、1 回の書き込みを原子的な 1 ステップにしている。
- 送信と spawn の成否、`park set` の成否、セッションの終了、daemon の生き死には非決定。各 mod が同じメモを渡そうとする
  回数は `MaxAttempts` まで。
- `park set` の戻しが失敗したメモは handed のまま残る (人が `park set` で status open に戻す)。これは性質を破らない
  (届いていないメモが handed なだけ) ので、モデルでも失敗を許している。
- 始まりの状態では daemon がそのセッションを生きていると見ている。タスクの記録より古い status.json は Adopt が受け付けない
  ので、「記録はあるのに daemon がまだそのセッションを見ていない」状態は始まりに置かない。
- compact はターンの間に走り、tool の hook と交錯しない。モデルは compact を 1 ステップにし、どの時点でも起きうる
  (hook の途中にも挟まる) ことにして、実際より広く探している。
- hook は PostToolUse で、同じターンの並列の tool の hook は順序が決まらないとしている。Claude Code のタスクの id は
  セッションの中で 1 から順に増える整数としている (`tasklog` の cc-premise)。
- housekeeping (mtime による削除) は置いていない。1 回の受け渡しの間に上の不変条件と絡まないため。
- `EdgeBind.tla` は辺 1 本と、`edge add` と `edge rm` と結びつけを 1 つずつ置く。結びつけるセッションの選び方
  (台帳の first_seen と辺の created_at、`ledger.Pick`) は非決定にし、現れたセッションのどれでも選べることにして広く
  探す。`edge rm` の os.Remove は 1 ステップ。
- 不変条件は `Mods = {m1}` でも回る。取ってから送る形は、mod 同士の取り合いだけでなく同じ mod の押し直しも閉じる。
