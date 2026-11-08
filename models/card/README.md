# 依存図のカードの状態のモデル (TLA+)

セッションの記録 (`tasks/`、`work/`、`edges/`) の更新と保持期間の掃除から、カードの状態を実装と同じ式で導いたもの (#483)。
回し方と期待の書き方は [handoff の README](../handoff/README.md) と同じ (`task model-check`)。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `Card.cfg` | `LivePlace`: 生きているセッションに活動の置き場がある | 進行中の作業が無ければ、作業の記録か pane からカードを作る |
| | `NoFalseExited`: 生きているセッションを終了と見せない | 状態は pane の有無で決まる |
| | `NoResurrectedExited`: 全部終えて終了したセッションを終了のカードで戻さない | 作業の記録が消えるまで完了タスクを残す |
| | `SatisfiedStays`: 上のセッションを待つ辺は済んだまま変わらない | 同上 |
| | `EventuallyLeaves` (liveness): 終了したセッションのカードはいずれ抜ける | 作業の記録が保持期間で消えると、残した完了タスクも消える |
| `CardNoKeep.cfg` | `NoResurrectedExited` が破れる (`violated` を期待) | 完了タスクを 24 時間で消すと、タスクの無いセッションと区別できない |
| `CardNoKeepEdge.cfg` | `SatisfiedStays` が破れる (`violated` を期待) | 同上。辺の上流が完了から終了へ変わる |

同じ性質を実装に対してテストが持つ。

| 性質 | テスト |
| ---- | ---- |
| `LivePlace` | `deps/work_test.go` の `TestBuild_liveSessionWithoutInProgressWork` |
| `NoResurrectedExited` / `SatisfiedStays` | `daemon/housekeeping_test.go` の `TestHousekeep_keepsCompletedTasksOfExitedSession`、`tasklog/prune_test.go` の `TestPrune_keepCompleted` |

## 前提

- 経過は保持期間の区切りだけを写す (1 日未満、7 日未満、7 日以上)。
- daemon は動いている前提。止まっている間はカードが全部 exited になる (`deps.Write` のコメント)。
- 辺は終了の前に書かれたものだけを写す。終了の後に足した辺は、作業の記録より長く残りうる。
- blockedBy は置かない。記録のあるタスクは全部が末端になる。
- housekeeping は作業の記録、辺、タスクの順に 1 回で消す。逆にすると、作業の記録が消えて完了タスクだけが残る窓ができ、
  その間に resume したセッションの活動の置き場が無くなる (`LivePlace` の反例で見つけた)。
