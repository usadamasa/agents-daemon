----------------------------- MODULE PRLink -----------------------------
(* PR を出したセッションと、その PR の参照のノードを結ぶ辺 (sid:A → pr:P) を daemon が導く手順を写したモデル (#487)。

   - PR P の参照は、渡す側が書く辺 pr:P → task:H/1 (prTask) の端点に出る。H は渡した側のセッション。
   - A は P の head branch で作業するセッション。A 自身が P のマージを待つタスクを持つと、辺 pr:P → task:A/1
     (prOwn) が書かれうる (自分の PR を自分で待つ)。
   - daemon の tick の引き直し (refpoll の Refresh) を 1 ステップにする。写しが無いか open なら gh で引き直し
     (状態と head branch を一緒に写す)、導き (Anchor) と、自分の PR を待つ形になった導いた辺の除去 (Unanchor) と、
     済んだ参照を before に持つ辺の掃除 (Sweep) をこの順に行う。gh の失敗と写しの保持期間は models/refstate が持つ
     ので、ここでは引き直しはいつも成功する。
   - 導きは、写しが head branch を持ち (写しがある)、P がどこかの辺の端点にあり、P を after に持つ辺が無く、
     生きているセッションのうち repo と branch の合うものが A だけで、pr:P → task:A/x の辺が無いとき
     sid:A → pr:P を書く (印 derived 付き)。edges/.lock の中で読んで書くので、`edge add` との交錯は 1 ステップで足りる。
   - DeriveFirst が FALSE の変種は、掃除の後に導く (性質を破る壊れたモデル)。
   - CheckSelfWait が FALSE の変種は、自分の PR を待つ辺を見ずに導き、導いた辺も消さない (性質を破る壊れたモデル)。
   - 人 (渡す側、A 自身) は辺をいつでも書き消しうる。導いた辺を `edge rm` しても、条件が揃えば次の tick で導き直す。 *)
EXTENDS Naturals

CONSTANTS
  DeriveFirst,    \* 掃除の前に導くか
  CheckSelfWait,  \* 自分の PR を待つ辺を見るか
  MaxOps          \* 人の操作の回数

None == "none"
Done == {"merged"}

VARIABLES
  remote,    \* GitHub の側の P の状態
  cache,     \* refs/ の写し (状態と head branch)。無ければ None
  live,      \* A が生きているか
  onBranch,  \* A の cwd が P の head branch か
  prTask,    \* 辺 pr:P → task:H/1
  prOwn,     \* 辺 pr:P → task:A/1
  sidPr,     \* 辺 sid:A → pr:P。none / derived (daemon が導いた) / manual (人が書いた)
  ops,       \* 人の操作の数
  chance,    \* tick の始めに導く条件が揃ったことがあるか (履歴)
  humanRm,   \* 人が sid:A → pr:P を消したことがあるか (履歴)
  selfSeen,  \* pr:P → task:A/1 が書かれたことがあるか (履歴)
  daemonLoop \* tick の終わりに、導いた辺が自分の PR を待つ辺と輪を作っていたことがあるか (履歴)

vars == <<remote, cache, live, onBranch, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>

TypeOK ==
  /\ remote \in {"open", "merged"}
  /\ cache \in {None, "open", "merged"}
  /\ live \in BOOLEAN
  /\ onBranch \in BOOLEAN
  /\ prTask \in BOOLEAN
  /\ prOwn \in BOOLEAN
  /\ sidPr \in {None, "derived", "manual"}
  /\ ops \in 0..MaxOps
  /\ chance \in BOOLEAN
  /\ humanRm \in BOOLEAN
  /\ selfSeen \in BOOLEAN
  /\ daemonLoop \in BOOLEAN

\* 渡す側が pr:P → task:H/1 を書いた時点。A は P の branch で動いている。daemon はまだ引いていない。
Init ==
  /\ remote = "open"
  /\ cache = None
  /\ live = TRUE
  /\ onBranch = TRUE
  /\ prTask = TRUE
  /\ prOwn = FALSE
  /\ sidPr = None
  /\ ops = 0
  /\ chance = FALSE
  /\ humanRm = FALSE
  /\ selfSeen = FALSE
  /\ daemonLoop = FALSE

\* 導く条件。c は引き直した後の写し、t と o はその時点の辺。
CanAnchor(c, t, o) ==
  /\ c # None
  /\ t \/ o
  /\ sidPr = None
  /\ live /\ onBranch
  /\ (CheckSelfWait => ~o)

\* daemon の tick。引き直し、導き、導いた辺の除去、掃除。
Tick ==
  LET c == IF cache \in {None, "open"} THEN remote ELSE cache
      swept(x) == x /\ c \notin Done
      t1 == IF DeriveFirst THEN prTask ELSE swept(prTask)
      o1 == IF DeriveFirst THEN prOwn ELSE swept(prOwn)
      s1 == IF CanAnchor(c, t1, o1) THEN "derived" ELSE sidPr
      s2 == IF CheckSelfWait /\ s1 = "derived" /\ o1 THEN None ELSE s1
      o2 == swept(prOwn)
  IN
  /\ cache' = c
  /\ sidPr' = s2
  /\ prTask' = swept(prTask)
  /\ prOwn' = o2
  /\ chance' = (chance \/ CanAnchor(c, prTask, prOwn))
  /\ daemonLoop' = (daemonLoop \/ (s2 = "derived" /\ o2))
  /\ UNCHANGED <<remote, live, onBranch, ops, humanRm, selfSeen>>

Human(change) ==
  /\ ops < MaxOps
  /\ ops' = ops + 1
  /\ change

\* PR がマージされる。
Merge ==
  /\ remote = "open"
  /\ remote' = "merged"
  /\ UNCHANGED <<cache, live, onBranch, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>

\* A が終わる (起き直さない)。
End ==
  /\ live
  /\ live' = FALSE
  /\ UNCHANGED <<remote, cache, onBranch, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>

\* A が branch を切り替える・戻す。
Switch ==
  Human(/\ onBranch' = ~onBranch
        /\ UNCHANGED <<remote, cache, live, prTask, prOwn, sidPr, chance, humanRm, selfSeen, daemonLoop>>)

\* 渡す側が pr:P → task:H/1 を書く・消す。
ToggleTask ==
  Human(/\ prTask' = ~prTask
        /\ UNCHANGED <<remote, cache, live, onBranch, prOwn, sidPr, chance, humanRm, selfSeen, daemonLoop>>)

\* A 自身が pr:P → task:A/1 を書く。
AddOwn ==
  Human(/\ ~prOwn
        /\ prOwn' = TRUE
        /\ selfSeen' = TRUE
        /\ UNCHANGED <<remote, cache, live, onBranch, prTask, sidPr, chance, humanRm, daemonLoop>>)

\* 人が sid:A → pr:P を書く (導いた辺に書き直すと人の辺になる)。
AddManual ==
  Human(/\ sidPr # "manual"
        /\ sidPr' = "manual"
        /\ UNCHANGED <<remote, cache, live, onBranch, prTask, prOwn, chance, humanRm, selfSeen, daemonLoop>>)

\* 人が sid:A → pr:P を `edge rm` する。
RemoveSidPr ==
  Human(/\ sidPr # None
        /\ sidPr' = None
        /\ humanRm' = TRUE
        /\ UNCHANGED <<remote, cache, live, onBranch, prTask, prOwn, chance, selfSeen, daemonLoop>>)

Next ==
  \/ Tick \/ Merge \/ End
  \/ Switch \/ ToggleTask \/ AddOwn \/ AddManual \/ RemoveSidPr

\* tick は 5 秒ごとに来る。
Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* tick の始めに導く条件が揃ったら、人が消すか自分の PR を待つ形にならない限り、P は sid:A → pr:P で A につながった
\* まま残る (PR のマージで pr:P → task:H/1 が消えても、P のノードは図から消えない)。
PRKept == (chance /\ ~humanRm /\ ~selfSeen) => sidPr # None

\* daemon は自分の PR を待つ辺と輪になる辺を、tick の終わりに残さない。
NoDaemonLoop == ~daemonLoop

\* 導いた辺が自分の PR を待つ辺と輪になっても、いずれ解ける。
LoopResolved == (sidPr = "derived" /\ prOwn) ~> ~(sidPr = "derived" /\ prOwn)

\* 渡す側が pr:P → task:H/1 だけを書いても、A が P の branch で生きていて P が open の間に、いずれ A とつながる。
EventuallyAnchored ==
  [](prTask /\ live /\ onBranch /\ ~prOwn /\ remote = "open" /\ sidPr = None
     => <>(sidPr # None \/ ~prTask \/ ~live \/ ~onBranch \/ prOwn \/ remote # "open"))

=============================================================================
