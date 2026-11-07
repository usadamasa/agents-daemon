# 外部の参照の状態のモデル (TLA+)

辺の端点の issue / PR の状態を daemon が gh で引いて写し (`refs/`) に書き、依存図がそれを読んでノードの状態を決める
手順 (#416) と、済んだ参照を before に持つ辺を daemon が消す手順 (#422) を TLA+ に写したもの。回し方と期待の
突き合わせは `models/handoff/README.md` と同じ `task model-check`。ステップと実装の対応は `.tla` のコメントが持つ。

参照の閉じ方は 2 つに分ける。済んだ (merged の PR、completed で閉じた issue。写しの `merged` / `closed`) は完了にして
辺を消す。取りやめ (マージせずに閉じた PR、not planned / duplicate で閉じた issue。写しの `dropped`) は完了にせず、
辺も残す。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `RefState.cfg` | `NoFalseComplete`: 済んでいない参照 (open、取りやめ) を完了と見せない | gh が失敗したら写しを書かない。取りやめは `dropped` と書く。依存図は写しが merged / closed のときだけ完了にする |
| | `CacheSound`: 写しが閉じた状態なら GitHub の側も同じ | 閉じた状態は戻らないので、引いた値は書いた後も正しい |
| | `OnlyDoneRemoved`: 済んでいない参照 (open、取りやめ) を before に持つ辺を消さない | 写しが merged / closed のときだけ消す。`CacheSound` から、そのとき GitHub の側も済んでいる |
| | `EventuallyCompleted` (liveness): 済んだ参照はいずれ完了になる | 写しが無いか open の参照は引き直す。gh がいずれ成功する前提で、写しが消えても引き直して書く |
| | `EventuallyDropped` (liveness): 取りやめの参照はいずれ取りやめになる | 同上 |
| | `EdgeRemoved` (liveness): 済んだ参照を before に持つ辺はいずれ消える | 引き直しのたびに、写しが merged / closed の参照の辺を掃く。済んだ後に書いた辺も次の引き直しで消える |
| `RefStateFailAsClosed.cfg` | `NoFalseComplete` が破れる (`violated` を期待) | gh の失敗を closed と書くと、open の参照を完了と見せる。失敗では写しを書かない根拠 |
| `RefStateDroppedAsDone.cfg` | `NoFalseComplete` が破れる (`violated` を期待) | gh の state (CLOSED) だけを見ると、マージせずに閉じた PR を完了と見せる。マージしたか・閉じた理由で分ける根拠 |
| `RefStateNoRefetch.cfg` | `EventuallyCompleted` が破れる (`violated` を期待) | 写しが open の参照を引き直さないと、マージされた PR がいつまでも待ち (`waiting`) のまま。open を間引いて引き直す根拠 |
| `RefStateSweepOnTransition.cfg` | `EdgeRemoved` が破れる (`violated` を期待) | 写しが済んだ状態に変わった引き直しのときだけ消すと、済んだ PR へ後から書いた辺が残り続ける (merged は引き直さない)。毎回掃く根拠 |

同じ性質を実装に対してテストが持つ。

| 性質 | テスト |
| ---- | ---- |
| `NoFalseComplete` | `refpoll/refpoll_test.go` の `TestRefresh_ghの失敗` (失敗した参照の写しを書かない)、`TestGHState` (閉じた理由で分ける)、`deps/graph_test.go` の `TestGraph_refStates` |
| `CacheSound` | `refstate/refstate_test.go` の `TestStore_Put_知らない状態は書かない` |
| `OnlyDoneRemoved` | `refpoll/refpoll_test.go` の `TestRefresh_済んでいない参照の辺は残す` (取りやめを含む) |
| `EventuallyCompleted` / `EventuallyDropped` | `refpoll/refpoll_test.go` の `TestRefresh_間引きと引き直し` と `refstate/refstate_test.go` の `TestDue` |
| `EdgeRemoved` | `refpoll/refpoll_test.go` の `TestRefresh_済んだ参照の辺を消す` (済んだ後に書いた辺を含む) |

## 前提

- GitHub の側の状態は open から閉じた状態へ 1 回だけ移る。閉じた issue / PR を開き直すと、写しが保持期間で
  消えるまで閉じたまま残る (daemon は閉じた参照を引き直さない)。
- gh が無い・未認証のまま直されない間は、参照は外部のまま残る (safety は成り立ち、liveness は前提の外)。
- 間引きの間隔 (時間) は置かず、公平性 (gh の呼び出しはいずれ成功する) で表す。
- 書くのは daemon の 1 つの goroutine だけで、写しの書き込みは `statefile.WriteJSONAtomic` なので 1 ステップ。
- 辺は参照 1 つを before に持つ 1 本で、`edge add` は `MaxAdds` 回まで書き直せる。辺の before は結びつけで書き換わらない
  (`session:` の端点だけが結びつく) ので、消すかは写しだけで決まり、`edge add` との交錯は `edges/.lock` の中の 1 ステップ
  (models/handoff の `EdgeBind`) で足りる。
