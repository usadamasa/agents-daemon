----------------------------- MODULE PRRepoKey -----------------------------
(* セッション A の作業の記録 (work/) に残す repo の鍵を、いつ求めて誰が読むかを写したモデル (#606)。
   照合の相手 (PRAuthor) と tick の順序 (PRLink) は別のモジュールが持ち、ここでは A 1 つの cwd と worktree と
   作業の記録だけを見る。

   - A は repo R の main worktree で起動し、1 度だけ linked worktree へ入りうる (EnterWorktree) し、戻りうる。
   - worktree は消えうる (ExitWorktree の remove、終了時の後片付け、別のセッションの git worktree remove)。
     RemoveInside が FALSE なら、A が生きていてその worktree にいる間は消えない。
   - 鍵 (gitrepo.RepoKey) は main と生きている worktree では R、消えた worktree ではそのパス W になる (git が引けない)。
   - hook は生きている間の UserPromptSubmit / Stop (記録を作る)、gh pr create か gh stack submit の PostToolUse (記録があれば P を残す)、
     終了の途中の SessionEnd (記録があれば書き換える)。どれも hook の cwd を記録に書く。Stop は割り込みで来ないことがあり、
     hook はどれも起きなくてよい。
   - 記録の鍵を読むのは 3 か所。
       daemon は記録に P があれば、いつでも 1 度だけ辺を導く (Anchor)。A が終了した後でもよい。
       人は終了した A の作業をパーキングのメモへ移しうる (Adopt、parking の AdoptWork)。記録はそこで消える。
       依存図 (deps) は記録のある間、いつでも A のノードを repo の列へ置く (Placed)。
   - 鍵を求める形 (Mode):
       "anchor":      記録に鍵を残さず、導くときに記録の cwd から求める (#606 の修正前)。
       "always":      hook のたびに cwd から求め直す。
       "oncwd":       記録が新しいか cwd が変わったときだけ求める (ledger.Observe と同じ)。
       "oncwdexists": "oncwd" に加え、cwd が無くなっていて記録に鍵があれば求め直さない。
   - メモとノードの鍵の読み方 (AdoptKey、PlaceKey):
       "cwd":    記録の cwd から求める (メモ 20261115T003548-a4ffe588 の修正前)。
       "record": メモは記録の鍵 (無ければ移さない)。ノードは cwd を git で引ければその鍵、引けなければ記録の鍵。 *)
EXTENDS Naturals

CONSTANTS
  Mode,          \* 鍵を求める形
  RemoveInside,  \* A が生きていて worktree にいる間に、その worktree が消えうるか
  AdoptKey,      \* メモへ移すときの鍵の読み方
  PlaceKey       \* 依存図がノードを置くときの鍵の読み方

ASSUME Mode \in {"anchor", "always", "oncwd", "oncwdexists"}
ASSUME AdoptKey \in {"cwd", "record"}
ASSUME PlaceKey \in {"cwd", "record"}

None == "none"

VARIABLES
  phase,     \* A の段: live / ending (SessionEnd の hook の前後、後片付けの途中) / ended
  cwd,       \* A の今の cwd: main / wt
  wt,        \* worktree: none (まだ無い) / alive / gone
  rec,       \* A の作業の記録があるか
  recCwd,    \* 記録の cwd
  recRepo,   \* 記録の repo の鍵
  pr,        \* 記録に P を出したと残っているか
  anchored,  \* 導いた辺の before の repo の鍵。導いていなければ None
  memo       \* A の作業を移したメモの repo の鍵。移していなければ None

vars == <<phase, cwd, wt, rec, recCwd, recRepo, pr, anchored, memo>>

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
  /\ memo \in Keys

Init ==
  /\ phase = "live"
  /\ cwd = "main"
  /\ wt = "none"
  /\ rec = FALSE
  /\ recCwd = None
  /\ recRepo = None
  /\ pr = FALSE
  /\ anchored = None
  /\ memo = None

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
  /\ UNCHANGED <<phase, rec, recCwd, recRepo, pr, anchored, memo>>

Exit ==
  /\ phase = "live" /\ cwd = "wt"
  /\ cwd' = "main"
  /\ UNCHANGED <<phase, wt, rec, recCwd, recRepo, pr, anchored, memo>>

Remove ==
  /\ wt = "alive"
  /\ cwd = "main" \/ phase # "live" \/ RemoveInside
  /\ wt' = "gone"
  /\ UNCHANGED <<phase, cwd, rec, recCwd, recRepo, pr, anchored, memo>>

\* UserPromptSubmit / Stop: 記録が無ければ作る。
Turn ==
  /\ phase = "live"
  /\ rec' = TRUE
  /\ WriteCWD
  /\ UNCHANGED <<phase, cwd, wt, pr, anchored, memo>>

\* gh pr create か gh stack submit の PostToolUse: 記録があれば P を足す。
CreatePR ==
  /\ phase = "live" /\ rec /\ ~pr
  /\ pr' = TRUE
  /\ WriteCWD
  /\ UNCHANGED <<phase, cwd, wt, rec, anchored, memo>>

BeginEnd ==
  /\ phase = "live"
  /\ phase' = "ending"
  /\ UNCHANGED <<cwd, wt, rec, recCwd, recRepo, pr, anchored, memo>>

\* SessionEnd: 記録があるときだけ書き換える。
SessionEnd ==
  /\ phase = "ending" /\ rec
  /\ phase' = "ended"
  /\ WriteCWD
  /\ UNCHANGED <<cwd, wt, rec, pr, anchored, memo>>

\* SessionEnd の hook が来ないまま終わる (記録が無い、hook が失敗した)。
Finish ==
  /\ phase = "ending"
  /\ phase' = "ended"
  /\ UNCHANGED <<cwd, wt, rec, recCwd, recRepo, pr, anchored, memo>>

\* daemon が P から A への辺を導く (edge の Store.Anchor)。記録の鍵があればそれ、無ければ記録の cwd から求める。
\* 記録が消えると P も消えるので、記録のある間だけ導く。
Anchor ==
  /\ rec /\ pr /\ anchored = None
  /\ anchored' = IF recRepo # None THEN recRepo ELSE Key(recCwd)
  /\ UNCHANGED <<phase, cwd, wt, rec, recCwd, recRepo, pr, memo>>

\* 人が終了した A の作業をメモへ移す (parking の AdoptWork)。メモを書いてから記録を消す。
\* "record" では記録に鍵が無ければ移さない (エラーを返し、記録も残す)。
Adopt ==
  /\ phase = "ended" /\ rec /\ memo = None
  /\ AdoptKey = "record" => recRepo # None
  /\ memo' = IF AdoptKey = "record" THEN recRepo ELSE Key(recCwd)
  /\ rec' = FALSE /\ recCwd' = None /\ recRepo' = None /\ pr' = FALSE
  /\ UNCHANGED <<phase, cwd, wt, anchored>>

\* 終了した A は足踏みする。終了の前に行き詰まる状態は TLC の deadlock の検査が拾う。
Terminated ==
  /\ phase = "ended"
  /\ UNCHANGED vars

Next ==
  \/ Enter \/ Exit \/ Remove \/ Turn \/ CreatePR \/ BeginEnd \/ SessionEnd \/ Finish \/ Anchor \/ Adopt
  \/ Terminated

Spec == Init /\ [][Next]_vars

\* 依存図が記録から A のノードを置く repo の鍵 (deps の locator)。tick ごとに組み直すので、状態の関数にする。
Placed ==
  IF PlaceKey = "cwd" \/ Exists(recCwd) \/ recRepo = None THEN Key(recCwd) ELSE recRepo

\* 導いた辺の before の repo の鍵は、いつ導いても本来のリポジトリ R で、worktree のパスにならない。
AnchoredRepo == anchored \in {None, "R"}

\* A の作業を移したメモの repo の鍵は、いつ移しても R。
MemoRepo == memo \in {None, "R"}

\* 記録のある間、依存図は A のノードをいつも R の列へ置く。
PlacedRepo == rec => Placed = "R"

=============================================================================
