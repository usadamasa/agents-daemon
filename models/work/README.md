# セッションの作業の記録のモデル (TLA+)

セッション 1 つの作業の記録 (`work/<session_id>.json`、`worklog`) を、hook (SessionStart / UserPromptSubmit / Stop /
SessionEnd)、companion (mod の推定の題)、人 (`work set`)、daemon (prune) が交錯して書く手順を TLA+ に写したもの (#368)。
回し方と期待の書き方は [handoff の README](../handoff/README.md) と同じ (`task model-check`)。

| モジュール | 写しているもの |
| ---- | ---- |
| `Work.tla` | 記録を hook が作り・更新し、companion が推定の題を足し、人が subject を直し、prune が消す。`Locked` で書き込みが lock の中の読んで書くの 1 ステップか、読んだ写しを別のステップで書くかを切り替える |

| cfg | 不変条件 / 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `Work.cfg` | `WorkAfterPrompt`: prompt を 1 度でも打ったセッションは記録を持つ | UserPromptSubmit の hook が記録が無ければ作る |
| | `HumanSubjectKept`: 人が直した subject を hook も companion も書き換えない | lock の中で今の記録を読み、subject は空のときだけ書く。companion は inferred だけを書く |
| | `EndedStays`: SessionEnd が書いた ended を遅れた Stop や companion が消さない | Stop と companion は ended に触れない。消すのは resume の SessionStart と prune だけ |
| | `NoLostUpdate`: hook と companion が交錯しても turns の更新を失わない | lock の中で読んでから書く |
| | `InferBounded`: companion は推定の題がある記録に重ねて走らず、回数の上限を越えない | 起こす前に記録を読んで確かめ、起こした回数を数える |
| | `EventuallyEnded` (liveness): 終了したセッションの記録は、生き返らなければいずれ ended になるか消える | SessionEnd の hook はいずれ動く (weak fairness) |
| `WorkRace.cfg` | `HumanSubjectKept` が破れる (`violated` を期待) | lock 無しで読んだ写しを書くと、間に人が直した subject を prompt の hook が戻す |
| `WorkRaceEnded.cfg` | `EndedStays` が破れる (`violated` を期待) | 同じく、SessionEnd の前に読んだ Stop の写しが ended を消す |
| `WorkRaceTurns.cfg` | `NoLostUpdate` が破れる (`violated` を期待) | 同じく、ターンの途中に入った delivery の UserPromptSubmit と Stop が同じ写しを書く |

同じ不変条件を、実物の Store へ hook の入力と `work set` を goroutine で交錯させる乱択テストが実装に対して持つ
(`task test` で回る)。

| 不変条件 | テスト |
| ---- | ---- |
| `HumanSubjectKept` / `EndedStays` / `NoLostUpdate` | `worklog/invariants_test.go` |
| `WorkAfterPrompt` (記録を作るのは UserPromptSubmit と Stop、SessionStart と SessionEnd は作らない) | `worklog/worklog_test.go` |
| `InferBounded` (mod の側: 推定の題がある記録に起こさない、回数は 2 回まで) | `hooks/work-infer.test.tsx` |

## 前提

- 同じセッションの hook は順に走るが、peer の delivery の UserPromptSubmit はターンの途中に入り Stop と前後しうる。モデルは
  動いていない hook をどの順でも動けることにして、実際より広く探している。
- hook はセッションの process の中で同期に走り切る (SessionStart は会話に入る前、UserPromptSubmit と Stop はターンの進みを
  止めて、SessionEnd は process が終わる前の 1.5 秒の予算で) ので、終了するときに残っている hook は無く、resume は SessionEnd の
  hook が残っている間は起きない。companion の推定だけは process の外の時間で進み、終了をまたぐ。この前提を外すと、遅れた
  prompt の hook が SessionEnd の後に記録を作る、resume の hook が SessionEnd の後に ended を消す、という順が見つかる
  (モデルを書いて最初に出た反例)。
  実機 (Claude Code 2.1.319) では、Stop の hook が 4 秒かかる間に /exit すると SessionEnd の hook が先に走り、Stop の hook は
  書き終える前に殺された (sleep の後に置くはずのファイルが無い)。つまり前提は「hook が終わるまで終了を待つ」ではなく
  「終了すると残りの hook は書かない」の形で成り立つ。バイナリの hook は数 ms で返るので重なる窓は小さく、重なっても
  lock の中の読んで書く (Locked) で ended を消さない (WorkRaceEnded は lock 無しの形の反例)。
  <!-- cc-premise: 2.1.319 hooks /exit は走っている Stop の hook を待たず、SessionEnd の hook を走らせて process を終える -->
- 記録を作るのは UserPromptSubmit と Stop の hook だけ。SessionStart と SessionEnd は記録があるときだけ書き換える
  (prompt を打たずに終わったセッションの記録を残さない)。
- 依存図のノードの「終了」(`exited`) は daemon の status.json に pane が無いことで決まり、記録の ended_at には乗らない。
  上の前提が外れて ended_at が書かれなくても、図の終了の表示と `task dismiss` / `park adopt` の門は崩れない。ended_at が
  効くのは終了の理由の表示だけ。
- `/clear` は SessionEnd (reason clear) の後に新しい session_id で SessionStart (source clear) が走る (実測)。別のセッションの
  記録になるので、モデルは 1 つの session_id だけを追う。
- companion の推定 (`$.model.fork`) は時間がかかり、その間にほかの hook と人の編集が入る。失敗 (API error、nothing-to-fork) は
  何も書かず、回数だけ数える。
- prune は終了したセッションの記録だけを消し、hook が残っている間は消さない (1 時間ごとの掃除に hook が跨ることは無い)。
  生きているセッションの記録が 7 日の無更新で消える場合は置いていない (Stop ごとに触るので起きない)。
- Stop の summary と cwd は性質に絡まないので写していない。
