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
   - daemon が見る A の branch は、git で引いた結果 (seen) を使い回した値になりうる (#577)。引くのは導く候補の PR が
     あり A が生きている tick だけ。使い回しの期限 (locateTTL) が切れるのは Expire、ほかの PR の照合で A の cwd を
     引くのは OtherLocate。Locate が引き直す時期を決める。
       "tick":  tick ごとに引く (使い回さない)。
       "ttl":   期限が切れたときだけ引く (#577 の修正前の実装)。
       "first": 加えて、P を初めて引いた tick と open から済んだ状態へ変わった tick に引く (issue の候補 1)。
       "done":  加えて、写しが済んだ状態の P を照合する tick に引く (掃除される直前の照合)。
   - HoldSweep が TRUE の変種は、写しが済んだ状態の P が導く候補の間、掃除を 1 tick 遅らせる (issue の候補 2)。
   - DeriveFirst が FALSE の変種は、掃除の後に導く (性質を破る壊れたモデル)。
   - CheckSelfWait が FALSE の変種は、自分の PR を待つ辺を見ずに導き、導いた辺も消さない (性質を破る壊れたモデル)。
   - 人 (渡す側、A 自身) は辺をいつでも書き消しうる。sid:A → pr:P を消す経路は CLI の `edge rm` とアプリの Unlink
     (中身は同じ `edge rm`) の 2 つ (#576)。Decline に入る経路で消すと、その組を「導かない」と記録し (declined)、
     導きは記録のある組を飛ばす。人が同じ組を `edge add` すると記録を外す。
       Decline = {}:             記録しない (#576 の修正前。条件が揃えば次の tick で導き直す)。
       Decline = {"app"}:        アプリの Unlink だけ記録する (issue の候補 2)。
       Decline = {"cli", "app"}: どちらの経路でも記録する (issue の候補 1)。
   - HideDerived が TRUE の変種は、アプリが導いた辺に Unlink を出さない (issue の候補 3)。
   - SplitDecline が TRUE の変種は、辺を消してから別のステップで記録する (lock の外で記録する壊れたモデル)。
   - 記録は保持期間で消える (ForgetDecline)。TouchDecline が TRUE なら、導きが記録で飛ばした tick に保持期間を
     数え直すので、導く条件が揃っている間は消えない。FALSE の変種はいつでも消える (性質を破る壊れたモデル)。 *)
EXTENDS Naturals

CONSTANTS
  DeriveFirst,    \* 掃除の前に導くか
  CheckSelfWait,  \* 自分の PR を待つ辺を見るか
  Locate,         \* A の branch を引き直す時期 ("tick" / "ttl" / "first" / "done")
  HoldSweep,      \* 済んだ候補の掃除を 1 tick 遅らせるか
  Decline,        \* 「導かない」を記録する、人が辺を消す経路 ({"cli", "app"} の部分集合)
  HideDerived,    \* アプリが導いた辺に Unlink を出さないか
  SplitDecline,   \* 辺を消すのと記録を別のステップで行うか
  TouchDecline,   \* 記録で導きを飛ばした tick に記録の保持期間を数え直すか
  MaxOps          \* 人の操作の回数

ASSUME Locate \in {"tick", "ttl", "first", "done"}
ASSUME Decline \subseteq {"cli", "app"}

None == "none"
Done == {"merged"}

VARIABLES
  remote,    \* GitHub の側の P の状態
  cache,     \* refs/ の写し (状態と head branch)。無ければ None
  live,      \* A が生きているか
  onBranch,  \* A の cwd が P の head branch か
  seen,      \* daemon が前に引いた A の branch が P の head branch か
  fresh,     \* seen が使い回しの期限内か
  held,      \* 前の tick で済んだ候補の掃除を遅らせたか
  prTask,    \* 辺 pr:P → task:H/1
  prOwn,     \* 辺 pr:P → task:A/1
  sidPr,     \* 辺 sid:A → pr:P。none / derived (daemon が導いた) / manual (人が書いた)
  ops,       \* 人の操作の数
  chance,    \* 掃除する tick の始めに導く条件が揃ったことがあるか (履歴)
  humanRm,   \* 人が sid:A → pr:P を消したことがあるか (履歴)
  selfSeen,  \* pr:P → task:A/1 が書かれたことがあるか (履歴)
  daemonLoop, \* tick の終わりに、導いた辺が自分の PR を待つ辺と輪を作っていたことがあるか (履歴)
  declined,  \* 組 (sid:A, pr:P) の「導かない」の記録があるか
  pending,   \* 辺を消した後、記録をまだ書いていないか (SplitDecline の変種だけ)
  rmHeld     \* 人が sid:A → pr:P を消してから、同じ組を書き直しも記録の期限切れも無いか (履歴)

vars == <<remote, cache, live, onBranch, seen, fresh, held, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop,
          declined, pending, rmHeld>>

Decl == <<declined, pending, rmHeld>>

TypeOK ==
  /\ remote \in {"open", "merged"}
  /\ cache \in {None, "open", "merged"}
  /\ live \in BOOLEAN
  /\ onBranch \in BOOLEAN
  /\ seen \in BOOLEAN
  /\ fresh \in BOOLEAN
  /\ held \in BOOLEAN
  /\ prTask \in BOOLEAN
  /\ prOwn \in BOOLEAN
  /\ sidPr \in {None, "derived", "manual"}
  /\ ops \in 0..MaxOps
  /\ chance \in BOOLEAN
  /\ humanRm \in BOOLEAN
  /\ selfSeen \in BOOLEAN
  /\ daemonLoop \in BOOLEAN
  /\ declined \in BOOLEAN
  /\ pending \in BOOLEAN
  /\ rmHeld \in BOOLEAN

\* 渡す側が pr:P → task:H/1 を書いた時点。A は P の branch で動いている。daemon はまだ引いていない。
\* daemon は A の cwd をまだ引いていない (fresh が FALSE)。前に引いた値の使い回しは OtherLocate と Switch で作る。
Init ==
  /\ remote = "open"
  /\ cache = None
  /\ live = TRUE
  /\ onBranch = TRUE
  /\ seen = FALSE
  /\ fresh = FALSE
  /\ held = FALSE
  /\ prTask = TRUE
  /\ prOwn = FALSE
  /\ sidPr = None
  /\ ops = 0
  /\ chance = FALSE
  /\ humanRm = FALSE
  /\ selfSeen = FALSE
  /\ daemonLoop = FALSE
  /\ declined = FALSE
  /\ pending = FALSE
  /\ rmHeld = FALSE

\* 記録を見ない導く条件。c は引き直した後の写し、t と o はその時点の辺、b は daemon が見る A の branch。
Derivable(c, t, o, b) ==
  /\ c # None
  /\ t \/ o
  /\ sidPr = None
  /\ live /\ b
  /\ (CheckSelfWait => ~o)

\* 導く条件。「導かない」の記録のある組は飛ばす。
CanAnchor(c, t, o, b) == Derivable(c, t, o, b) /\ ~declined

\* daemon の tick。引き直し、導き、導いた辺の除去、掃除。
Tick ==
  LET c == IF cache \in {None, "open"} THEN remote ELSE cache
      \* 導く候補か (写しが head branch を持ち、P が辺の before にあり、after に無い)。導く前の辺で決める。
      cand == c # None /\ (prTask \/ prOwn) /\ sidPr = None
      \* A の cwd を照合するか (候補があり A が生きているときだけ place を呼ぶ)。
      look == cand /\ live
      reread == CASE Locate = "tick" -> TRUE
                  [] Locate = "ttl" -> ~fresh
                  [] Locate = "first" -> ~fresh \/ cache = None \/ (cache = "open" /\ c \in Done)
                  [] Locate = "done" -> ~fresh \/ c \in Done
      b == IF reread THEN onBranch ELSE seen
      hold == HoldSweep /\ c \in Done /\ cand /\ ~held
      swept(x) == x /\ (c \notin Done \/ hold)
      t1 == IF DeriveFirst THEN prTask ELSE swept(prTask)
      o1 == IF DeriveFirst THEN prOwn ELSE swept(prOwn)
      s1 == IF look /\ CanAnchor(c, t1, o1, b) THEN "derived" ELSE sidPr
      s2 == IF CheckSelfWait /\ s1 = "derived" /\ o1 THEN None ELSE s1
      o2 == swept(prOwn)
  IN
  /\ cache' = c
  /\ seen' = IF look /\ reread THEN onBranch ELSE seen
  /\ fresh' = IF look /\ reread THEN TRUE ELSE fresh
  /\ held' = hold
  /\ sidPr' = s2
  /\ prTask' = swept(prTask)
  /\ prOwn' = o2
  /\ chance' = (chance \/ (c \in Done /\ CanAnchor(c, prTask, prOwn, onBranch)))
  /\ daemonLoop' = (daemonLoop \/ (s2 = "derived" /\ o2))
  /\ UNCHANGED <<remote, live, onBranch, ops, humanRm, selfSeen>>
  /\ UNCHANGED Decl

\* 使い回しの期限 (locateTTL) が切れる。
Expire ==
  /\ Locate # "tick"
  /\ fresh
  /\ fresh' = FALSE
  /\ UNCHANGED <<remote, cache, live, onBranch, seen, held, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>
  /\ UNCHANGED Decl

\* ほかの PR を照合する tick で、daemon が A の cwd の branch を引く。
OtherLocate ==
  /\ Locate # "tick"
  /\ live /\ ~fresh
  /\ seen' = onBranch
  /\ fresh' = TRUE
  /\ UNCHANGED <<remote, cache, live, onBranch, held, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>
  /\ UNCHANGED Decl

\* SplitDecline の変種で、消した後の別のステップで記録を書く。
WriteDecline ==
  /\ pending
  /\ declined' = TRUE
  /\ pending' = FALSE
  /\ UNCHANGED <<remote, cache, live, onBranch, seen, fresh, held, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop,
                 rmHeld>>

\* 記録が保持期間 (housekeeping の Prune) で消える。時刻は持たず、いつ起きてもよい動作にする。TouchDecline なら
\* 記録で導きを飛ばす tick に保持期間を数え直すので、記録が無ければ導く状態 (A の今の branch でも daemon が
\* 前に引いた branch でも) の間は消えない。導く条件が途切れてから消えた記録は、人の判断の期限切れとして
\* 履歴 rmHeld を下ろす (条件が揃い直したら導いてよい)。
Quiet == ~(Derivable(cache, prTask, prOwn, onBranch) \/ Derivable(cache, prTask, prOwn, seen))

ForgetDecline ==
  /\ declined
  /\ TouchDecline => Quiet
  /\ declined' = FALSE
  /\ rmHeld' = (rmHeld /\ ~Quiet)
  /\ UNCHANGED <<remote, cache, live, onBranch, seen, fresh, held, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop,
                 pending>>

Human(change) ==
  /\ ops < MaxOps
  /\ ops' = ops + 1
  /\ change

Daemon == <<seen, fresh, held>>

\* PR がマージされる。
Merge ==
  /\ remote = "open"
  /\ remote' = "merged"
  /\ UNCHANGED <<cache, live, onBranch, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>
  /\ UNCHANGED Daemon
  /\ UNCHANGED Decl

\* A が終わる (起き直さない)。
End ==
  /\ live
  /\ live' = FALSE
  /\ UNCHANGED <<remote, cache, onBranch, prTask, prOwn, sidPr, ops, chance, humanRm, selfSeen, daemonLoop>>
  /\ UNCHANGED Daemon
  /\ UNCHANGED Decl

\* A が branch を切り替える・戻す。
Switch ==
  Human(/\ onBranch' = ~onBranch
        /\ UNCHANGED <<remote, cache, live, prTask, prOwn, sidPr, chance, humanRm, selfSeen, daemonLoop>>
        /\ UNCHANGED Daemon
        /\ UNCHANGED Decl)

\* 渡す側が pr:P → task:H/1 を書く・消す。
ToggleTask ==
  Human(/\ prTask' = ~prTask
        /\ UNCHANGED <<remote, cache, live, onBranch, prOwn, sidPr, chance, humanRm, selfSeen, daemonLoop>>
        /\ UNCHANGED Daemon
        /\ UNCHANGED Decl)

\* A 自身が pr:P → task:A/1 を書く。
AddOwn ==
  Human(/\ ~prOwn
        /\ prOwn' = TRUE
        /\ selfSeen' = TRUE
        /\ UNCHANGED <<remote, cache, live, onBranch, prTask, sidPr, chance, humanRm, daemonLoop>>
        /\ UNCHANGED Daemon
        /\ UNCHANGED Decl)

\* 人が sid:A → pr:P を書く (導いた辺に書き直すと人の辺になる)。同じ組の「導かない」の記録を外す。
AddManual ==
  Human(/\ sidPr # "manual"
        /\ sidPr' = "manual"
        /\ declined' = FALSE
        /\ pending' = FALSE
        /\ rmHeld' = FALSE
        /\ UNCHANGED <<remote, cache, live, onBranch, prTask, prOwn, chance, humanRm, selfSeen, daemonLoop>>
        /\ UNCHANGED Daemon)

\* 人が sid:A → pr:P を経路 via ("cli" は `edge rm`、"app" はアプリの Unlink) で消す。導いた辺も人の辺も消せる。
\* HideDerived ならアプリは導いた辺に Unlink を出さない。Decline に入る経路なら、同じステップで (SplitDecline なら
\* 後の WriteDecline で) 「導かない」を記録する。
RemoveSidPr(via) ==
  Human(/\ sidPr # None
        /\ (via = "app" /\ HideDerived => sidPr = "manual")
        /\ sidPr' = None
        /\ humanRm' = TRUE
        /\ rmHeld' = TRUE
        /\ declined' = (declined \/ (via \in Decline /\ ~SplitDecline))
        /\ pending' = (pending \/ (via \in Decline /\ SplitDecline))
        /\ UNCHANGED <<remote, cache, live, onBranch, prTask, prOwn, chance, selfSeen, daemonLoop>>
        /\ UNCHANGED Daemon)

Next ==
  \/ Tick \/ Expire \/ OtherLocate \/ Merge \/ End
  \/ WriteDecline \/ ForgetDecline
  \/ Switch \/ ToggleTask \/ AddOwn \/ AddManual
  \/ \E via \in {"cli", "app"} : RemoveSidPr(via)

\* tick は 5 秒ごとに来て、使い回しの期限は 1 分で切れる。
Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Expire)

\* 写しが済んだ状態の P を掃除する tick の始めに導く条件が揃ったら、人が消すか自分の PR を待つ形にならない限り、
\* P は sid:A → pr:P で A につながったまま残る (PR のマージで pr:P → task:H/1 が消えても、P のノードは図から消えない)。
\* 写しが open の間は掃除しないので、導くのが遅れても辺は残り、EventuallyAnchored が導くことを持つ。
PRKept == (chance /\ ~humanRm /\ ~selfSeen) => sidPr # None

\* daemon は自分の PR を待つ辺と輪になる辺を、tick の終わりに残さない。
NoDaemonLoop == ~daemonLoop

\* 導いた辺が自分の PR を待つ辺と輪になっても、いずれ解ける。
LoopResolved == (sidPr = "derived" /\ prOwn) ~> ~(sidPr = "derived" /\ prOwn)

\* 渡す側が pr:P → task:H/1 だけを書いても、A が P の branch で生きていて P が open の間に、いずれ A とつながる。
\* 人が「導かない」と記録した組は除く (NoRederive と両立させるため、人の判断を導きより優先する)。
EventuallyAnchored ==
  [](prTask /\ live /\ onBranch /\ ~prOwn /\ remote = "open" /\ sidPr = None /\ ~declined /\ ~pending
     => <>(sidPr # None \/ ~prTask \/ ~live \/ ~onBranch \/ prOwn \/ remote # "open" \/ declined \/ pending))

\* 人が sid:A → pr:P を消したら、人が同じ組を書き直すか、導く条件が途切れて記録が期限切れになるまで、daemon は
\* その組を導き直さない (#576)。CLI の `edge rm` でもアプリの Unlink でも同じ。
NoRederive == rmHeld => sidPr # "derived"

=============================================================================
