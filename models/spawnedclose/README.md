# 起こした session を畳む close のモデル (TLA+)

`agents-daemon spawned-session close` (`parkcli` の `closeSpawnedSession`) と、同じ session を閉じうる包みの `c()`、
daemon の reaper、利用者の交錯を TLA+ に写したもの (#596)。
回し方と期待の突き合わせは `models/handoff/README.md` と同じ `task model-check`。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `SpawnedClose.cfg` | `NoKillUnmarked`: close は印を書いたことの無い session を閉じない | 印の無い session が rex に居れば拒否する |
| | `FoldedIsOk` / `FoldedSpawnedIsOk`: 打った時点で畳まれていた session への close は、rex が落ちていなければ `ok_gone` | 印より先に rex を数え、無ければ印の有無に依らず成功にする |
| | `ErrOnlyWhenDownOrRefused`: 失敗は rex が落ちたときか、印の無い生きた session を拒否したときだけ | kill に失敗したら数え直し、無ければ成功にする |
| | `OkMeansGone`: 成功と返したら session は rex に無い | 接続の失敗は target not found と別の error で、成功に落ちない |
| | `MarkKeptWhileLive`: 生きている起こした session の印を消さない | 印を消すのは session が無いと分かったときか閉じたときだけ |
| | `MarkedLiveGetsClosed` (liveness): 打った時点で印があり生きていた session は、rex が落ちない限り最終的に無くなる | close の各段に弱い公平性 (WF) を置く。他の主体には置かない |
| | `CloseTerminates` (liveness): close は必ず終わる | 同上 |
| `SpawnedCloseNeedsMark.cfg` | `FoldedSpawnedIsOk` が破れる (`violated` を期待) | 修正前の形。印を書いた session が閉じ、reaper が印を消した後の close が exit 1 になる |
| `SpawnedCloseNoRecount.cfg` | `ErrOnlyWhenDownOrRefused` が破れる | 数えた後、kill の前に別の主体が閉じると kill が失敗して exit 1 になる。数え直す根拠 |
| `SpawnedCloseDownAsGone.cfg` | `OkMeansGone` が破れる | 接続の失敗を not found と読むと、生きた session を「もう無い」と返して印を消す |
| `SpawnedCloseIgnoreMark.cfg` | `NoKillUnmarked` が破れる | 印を見ずに閉じると起こしていない session を閉じる。`NoKillUnmarked` が空振りしない確認 |

## 実装との対応

| モデルの動作 | 実装 |
| ---- | ---- |
| `WMark` | 包みの sh (`rexcli` の `closeOnExitScript`) が打つ `spawned-session mark` → `spawnedsession.Store.Put` |
| `WExit`、`WClean` | 包みの sh の claude の終了と `c()` (`block ls` で自分の block だけなら `session kill`) |
| `Reap` | `reaper.reapOne` (not found なら印を消す、block が 0 なら閉じて印を消す) |
| `CloseStart`、`CGetMark` | `closeSpawnedSession` の入口と `store.Get` |
| `CCount` | `closer.SessionBlocks` (`rexcli` の `execClient.SessionBlocks`) と、その結果の振り分け |
| `CKill` | `killOrGone` の `closer.SessionKill` |
| `CRecount` | `killOrGone` の数え直し |
| `Found`、`CountFails` | `SessionBlocks` が target not found のときだけ `found=false`、ほかの失敗は error |

同じ性質を実装に対して `parkcli/spawned_test.go` が持つ。

| 性質 | テスト |
| ---- | ---- |
| `NoKillUnmarked` | `TestCloseSpawnedSession_RefusesUnmarked` |
| `FoldedIsOk` | `TestCloseSpawnedSession_AlreadyGone`、`TestCloseSpawnedSession_UnmarkedAlreadyGone` |
| `ErrOnlyWhenDownOrRefused` | `TestCloseSpawnedSession_KillRaceGone` |
| `OkMeansGone` | `TestCloseSpawnedSession_UnmarkedCountErr`、`TestCloseSpawnedSession_FailureKeepsMark` |

## 前提

- 起こした session 2 つ、起こしていない session 1 つ。close は 1 回だけ打つ。
- 各 session の pane は 1 つに縮める。`c()` の「ほかの pane が残っていた」は、閉じずに pane だけ消える非決定の分岐で表す。
- rex の server は落ちたら戻らない。落ちた後の問い合わせはどれも接続の失敗。
- 閉じた session は戻らない。rex は同じ session ID を作り直さない。
- reaper の数えると閉じるは 1 ステップにまとめる。reaper 自身の競合はこのモデルの対象外。
- 印の読み書き (`spawnedsession` のファイル) は 1 ステップで、書きかけは見えない (`statefile.WriteJSONAtomic`)。
- close の段にだけ弱い公平性を置く。包み、reaper、利用者、server の停止は止まってもよい。
