----------------------------- MODULE PRRepoKey -----------------------------
(* PR を出したセッション A から導く辺 sid:A → pr:P の before の repo の鍵を、いつ求めるかを写したモデル (#606)。
   照合の相手 (PRAuthor) と tick の順序 (PRLink) は別のモジュールが持ち、ここでは A 1 つの cwd と worktree と
   作業の記録 (work/) だけを見る。

   - A は repo R の main worktree で起動し、1 度だけ linked worktree へ入りうる (EnterWorktree) し、戻りうる。
   - worktree は消えうる (ExitWorktree の remove、終了時の後片付け、別のセッションの git worktree remove)。
     RemoveInside が FALSE なら、A が生きていてその worktree にいる間は消えない。
   - 鍵 (gitrepo.RepoKey) は main と生きている worktree では R、消えた worktree ではそのパス W になる (git が引けない)。
   - hook は生きている間の UserPromptSubmit / Stop (記録を作る)、gh pr create の PostToolUse (記録があれば P を残す)、
     終了の途中の SessionEnd (記録があれば書き換える)。どれも hook の cwd を記録に書く。Stop は割り込みで来ないことがあり、
     hook はどれも起きなくてよい。
   - daemon は記録に P があれば、いつでも 1 度だけ辺を導く (Anchor)。A が終了した後でもよい。
   - 鍵を求める形 (Mode):
       "anchor":      記録に鍵を残さず、導くときに記録の cwd から求める (#606 の修正前)。
       "always":      hook のたびに cwd から求め直す。
       "oncwd":       記録が新しいか cwd が変わったときだけ求める (ledger.Observe と同じ)。
       "oncwdexists": "oncwd" に加え、cwd が無くなっていて記録に鍵があれば求め直さない。 *)
EXTENDS Naturals

CONSTANTS
  Mode,          \* 鍵を求める形
  RemoveInside   \* A が生きていて worktree にいる間に、その worktree が消えうるか

ASSUME Mode \in {"anchor", "always", "oncwd", "oncwdexists"}

None == "none"

VARIABLES
  phase,     \* A の段: live / ending (SessionEnd の hook の前後、後片付けの途中) / ended
  cwd,       \* A の今の cwd: main / wt
  wt,        \* worktree: none (まだ無い) / alive / gone
  rec,       \* A の作業の記録があるか
  recCwd,    \* 記録の cwd
  recRepo,   \* 記録の repo の鍵
  pr,        \* 記録に P を出したと残っているか
  anchored   \* 導いた辺の before の repo の鍵。導いていなければ None

vars == <<phase, cwd, wt, rec, recCwd, recRepo, pr, anchored>>

Keys == {None, "R", "W"}

TypeOK ==
  /\ phase \in {"live", "ending", "ended"}
  /\ cwd \in {"main", "wt"}
  /\ wt \in {"none", "alive", "gone"}
  /\ rec \in BOOLEAN
  /\ recCwd \in {None, "main", "wt"}
  /\ recRepo \in Keys
  /\ pr \in BOOLEAN
  /\ anchored \in Keys

Init ==
  /\ phase = "live"
  /\ cwd = "main"
  /\ wt = "none"
  /\ rec = FALSE
  /\ recCwd = None
  /\ recRepo = None
  /\ pr = FALSE
  /\ anchored = None

Exists(c) == c = "main" \/ wt = "alive"

\* gitrepo.RepoKey: git で引ければ main worktree のパス R、引けなければ cwd そのもの。
Key(c) == IF Exists(c) THEN "R" ELSE "W"

\* hook が cwd c を記録に書いたときの記録の鍵。
NewRepo(c) ==
  CASE Mode = "anchor" -> None
    [] Mode = "always" -> Key(c)
    [] Mode = "oncwd" -> IF recCwd # c \/ recRepo = None THEN Key(c) ELSE recRepo
    [] Mode = "oncwdexists" ->
         IF recRepo # None /\ (recCwd = c \/ ~Exists(c)) THEN recRepo ELSE Key(c)

WriteCWD ==
  /\ recCwd' = cwd
  /\ recRepo' = NewRepo(cwd)

Enter ==
  /\ phase = "live" /\ wt = "none"
  /\ wt' = "alive" /\ cwd' = "wt"
  /\ UNCHANGED <<phase, rec, recCwd, recRepo, pr, anchored>>

Exit ==
  /\ phase = "live" /\ cwd = "wt"
  /\ cwd' = "main"
  /\ UNCHANGED <<phase, wt, rec, recCwd, recRepo, pr, anchored>>

Remove ==
  /\ wt = "alive"
  /\ cwd = "main" \/ phase # "live" \/ RemoveInside
  /\ wt' = "gone"
  /\ UNCHANGED <<phase, cwd, rec, recCwd, recRepo, pr, anchored>>

\* UserPromptSubmit / Stop: 記録が無ければ作る。
Turn ==
  /\ phase = "live"
  /\ rec' = TRUE
  /\ WriteCWD
  /\ UNCHANGED <<phase, cwd, wt, pr, anchored>>

\* gh pr create の PostToolUse: 記録があれば P を足す。
CreatePR ==
  /\ phase = "live" /\ rec /\ ~pr
  /\ pr' = TRUE
  /\ WriteCWD
  /\ UNCHANGED <<phase, cwd, wt, rec, anchored>>

BeginEnd ==
  /\ phase = "live"
  /\ phase' = "ending"
  /\ UNCHANGED <<cwd, wt, rec, recCwd, recRepo, pr, anchored>>

\* SessionEnd: 記録があるときだけ書き換える。
SessionEnd ==
  /\ phase = "ending" /\ rec
  /\ phase' = "ended"
  /\ WriteCWD
  /\ UNCHANGED <<cwd, wt, rec, pr, anchored>>

\* SessionEnd の hook が来ないまま終わる (記録が無い、hook が失敗した)。
Finish ==
  /\ phase = "ending"
  /\ phase' = "ended"
  /\ UNCHANGED <<cwd, wt, rec, recCwd, recRepo, pr, anchored>>

\* daemon が P から A への辺を導く (edge の Store.Anchor)。記録の鍵があればそれ、無ければ記録の cwd から求める。
Anchor ==
  /\ pr /\ anchored = None
  /\ anchored' = IF recRepo # None THEN recRepo ELSE Key(recCwd)
  /\ UNCHANGED <<phase, cwd, wt, rec, recCwd, recRepo, pr>>

\* 終了した A は足踏みする。終了の前に行き詰まる状態は TLC の deadlock の検査が拾う。
Terminated ==
  /\ phase = "ended"
  /\ UNCHANGED vars

Next ==
  \/ Enter \/ Exit \/ Remove \/ Turn \/ CreatePR \/ BeginEnd \/ SessionEnd \/ Finish \/ Anchor
  \/ Terminated

Spec == Init /\ [][Next]_vars

\* 導いた辺の before の repo の鍵は、いつ導いても本来のリポジトリ R で、worktree のパスにならない。
AnchoredRepo == anchored \in {None, "R"}

=============================================================================
