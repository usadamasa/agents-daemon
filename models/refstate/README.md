# 外部の参照の状態のモデル (TLA+)

辺の端点の issue / PR の状態を daemon が gh で引いて写し (`refs/`) に書き、依存図がそれを読んでノードの状態を決める
手順を TLA+ に写したもの (#416)。回し方と期待の突き合わせは `models/handoff/README.md` と同じ `task model-check`。
ステップと実装の対応は `.tla` のコメントが持つ。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `RefState.cfg` | `NoFalseComplete`: 済んでいない参照を完了と見せない | gh が失敗したら写しを書かない。依存図は写しが merged / closed のときだけ完了にする |
| | `CacheSound`: 写しが merged / closed なら GitHub の側も同じ | 済んだ状態は戻らないので、引いた値は書いた後も正しい |
| | `EventuallyCompleted` (liveness): 済んだ参照はいずれ完了になる | 写しが無いか open の参照は引き直す。gh がいずれ成功する前提で、写しが消えても引き直して書く |
| `RefStateFailAsClosed.cfg` | `NoFalseComplete` が破れる (`violated` を期待) | gh の失敗を closed と書くと、open の参照を完了と見せる。失敗では写しを書かない根拠 |
| `RefStateNoRefetch.cfg` | `EventuallyCompleted` が破れる (`violated` を期待) | 写しが open の参照を引き直さないと、マージされた PR がいつまでも外部のまま。open を間引いて引き直す根拠 |

同じ性質を実装に対してテストが持つ。

| 性質 | テスト |
| ---- | ---- |
| `NoFalseComplete` | `refpoll/refpoll_test.go` の `TestRefresh_ghの失敗` (失敗した参照の写しを書かない) |
| `CacheSound` | `refstate/refstate_test.go` の `TestStore_Put_知らない状態は書かない` |
| `EventuallyCompleted` | `refpoll/refpoll_test.go` の `TestRefresh_間引きと引き直し` と `refstate/refstate_test.go` の `TestDue` |

## 前提

- GitHub の側の状態は open から merged か closed へ 1 回だけ移る。close した issue を開き直すと、写しが保持期間で
  消えるまで完了のまま残る (daemon は merged / closed を引き直さない)。
- gh が無い・未認証のまま直されない間は、参照は外部のまま残る (safety は成り立ち、liveness は前提の外)。
- 間引きの間隔 (時間) は置かず、公平性 (gh の呼び出しはいずれ成功する) で表す。
- 書くのは daemon の 1 つの goroutine だけで、写しの書き込みは `statefile.WriteJSONAtomic` なので 1 ステップ。
