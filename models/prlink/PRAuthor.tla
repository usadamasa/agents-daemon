----------------------------- MODULE PRAuthor -----------------------------
(* PR P の辺 sid → pr:P を、どのセッションから導くかの照合を写したモデル (#575)。tick の順序 (導きと掃除)、
   branch の使い回し、自分の PR を待つ形、人が外した組は PRLink が持つので、ここでは照合の相手だけを見る。

   - A は P を出したセッション (Issuer)、B はほかのセッション。どちらも終了しうり、branch を移りうる。
     B は始めから P の branch にいてもよい (同じ branch の複数セッション、レビューのための checkout)。
   - daemon の tick を 1 ステップにする。写しが無いか open なら引き直し、P が辺 pr:P → task:H/1 (prTask) の
     before にあり、sid → pr:P の辺がまだ無ければ照合の相手 (Targets) へ導き、写しが済んだ状態なら prTask を消す。
     tick ごとに生きているセッションの今の branch を引く (使い回しは PRLink が持つ)。
   - 照合の形 (Match):
       "live":       生きているセッションのうち P の branch にいるものがちょうど 1 つならそれ (#575 の修正前)。
       "history":    終了したセッションも、最後の hook のときの branch で照合する (issue の候補 1)。
       "nearest":    合うものが 2 つ以上なら 1 つを選ぶ (issue の候補 3)。時刻の近さは出した者を決めないので、
                     どれを選ぶかは非決定にする。
       "record":     P を出したときに書いた記録 (rec) があればその持ち主、無ければ "live" (issue の候補 2)。
       "recordonly": 記録があるときだけ、その持ち主。
   - 記録は、A の PostToolUse の hook が `gh pr create` の出力の URL を A の作業の記録へ書いたもの。Recorded が FALSE の
     変種は、記録の無い PR (web で作った、記録を書く前のバージョンの plugin で作った)。Expire が TRUE なら、記録は
     A の終了後の保持期間で消える (Forget)。
   - Shared が FALSE の変種は、B が P の branch に入らない (A だけの終了と移動を見る)。
   - last はセッションの最後の hook のときの branch が P の branch だったか。hook はいつ起きてもよい (Hook)。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Match,     \* 照合の形
  Recorded,  \* P を出したときに記録を書いたか
  Expire,    \* A の終了後に記録が保持期間で消えうるか
  Shared,    \* B が P の branch に入りうるか
  MaxOps     \* 人の操作の回数

ASSUME Match \in {"live", "history", "nearest", "record", "recordonly"}

None == "none"
Done == {"merged"}
Issuer == "A"
S == {"A", "B"}

VARIABLES
  remote,    \* GitHub の側の P の状態
  cache,     \* refs/ の写し。無ければ None
  prTask,    \* 辺 pr:P → task:H/1
  live,      \* セッションごとの、生きているか
  onBranch,  \* セッションごとの、今の cwd が P の head branch か
  last,      \* セッションごとの、最後の hook のときの branch が P の head branch か
  rec,       \* A の作業の記録に P を出した記録があるか
  sidPr,     \* セッションごとの、辺 sid → pr:P (none / derived)
  ops,       \* 人の操作の数
  lost       \* 記録が残っている間に、A につながらないまま prTask が掃除されたことがあるか (履歴)

vars == <<remote, cache, prTask, live, onBranch, last, rec, sidPr, ops, lost>>

TypeOK ==
  /\ remote \in {"open", "merged"}
  /\ cache \in {None, "open", "merged"}
  /\ prTask \in BOOLEAN
  /\ live \in [S -> BOOLEAN]
  /\ onBranch \in [S -> BOOLEAN]
  /\ last \in [S -> BOOLEAN]
  /\ rec \in BOOLEAN
  /\ sidPr \in [S -> {None, "derived"}]
  /\ ops \in 0..MaxOps
  /\ lost \in BOOLEAN

\* A が P を出し (出した直後の hook は P の branch を見る)、渡す側が pr:P → task:H/1 を書いた時点。daemon はまだ引いていない。
Init ==
  /\ remote = "open"
  /\ cache = None
  /\ prTask = TRUE
  /\ live = [s \in S |-> TRUE]
  /\ onBranch \in {f \in [S -> BOOLEAN] : f[Issuer] /\ (f["B"] => Shared)}
  /\ last = onBranch
  /\ rec = Recorded
  /\ sidPr = [s \in S |-> None]
  /\ ops = 0
  /\ lost = FALSE

Unique(m) == IF Cardinality(m) = 1 THEN m ELSE {}

LiveMatch == {s \in S : live[s] /\ onBranch[s]}
HistoryMatch == {s \in S : IF live[s] THEN onBranch[s] ELSE last[s]}

\* 導く相手の候補。2 つ以上なら、そのうちのどれか 1 つへ導く (nearest だけが 2 つ以上を返す)。
Targets ==
  CASE Match = "live" -> Unique(LiveMatch)
    [] Match = "history" -> Unique(HistoryMatch)
    [] Match = "nearest" -> LiveMatch
    [] Match = "record" -> IF rec THEN {Issuer} ELSE Unique(LiveMatch)
    [] Match = "recordonly" -> IF rec THEN {Issuer} ELSE {}

Tick ==
  LET c == IF cache \in {None, "open"} THEN remote ELSE cache
      cand == c # None /\ prTask /\ \A s \in S : sidPr[s] = None
  IN
  /\ cache' = c
  /\ IF cand /\ Targets # {}
       THEN \E t \in Targets : sidPr' = [sidPr EXCEPT ![t] = "derived"]
       ELSE UNCHANGED sidPr
  /\ prTask' = (prTask /\ c \notin Done)
  /\ lost' = (lost \/ (prTask /\ c \in Done /\ rec /\ sidPr'[Issuer] = None))
  /\ UNCHANGED <<remote, live, onBranch, last, rec, ops>>

\* セッションの hook (UserPromptSubmit、Stop など) が今の branch を記録する。
Hook(s) ==
  /\ live[s]
  /\ last[s] # onBranch[s]
  /\ last' = [last EXCEPT ![s] = onBranch[s]]
  /\ UNCHANGED <<remote, cache, prTask, live, onBranch, rec, sidPr, ops, lost>>

Merge ==
  /\ remote = "open"
  /\ remote' = "merged"
  /\ UNCHANGED <<cache, prTask, live, onBranch, last, rec, sidPr, ops, lost>>

\* セッションが終わる (起き直さない)。
End(s) ==
  /\ live[s]
  /\ live' = [live EXCEPT ![s] = FALSE]
  /\ UNCHANGED <<remote, cache, prTask, onBranch, last, rec, sidPr, ops, lost>>

\* A の作業の記録が、A の終了から保持期間が過ぎて消える (生きている間は daemon が mtime を進めるので消えない)。
Forget ==
  /\ Expire
  /\ rec /\ ~live[Issuer]
  /\ rec' = FALSE
  /\ UNCHANGED <<remote, cache, prTask, live, onBranch, last, sidPr, ops, lost>>

Human(change) ==
  /\ ops < MaxOps
  /\ ops' = ops + 1
  /\ change

\* セッションが branch を移る・戻る (ExitWorktree、checkout)。
Switch(s) ==
  Human(/\ live[s]
        /\ (s = Issuer \/ Shared)
        /\ onBranch' = [onBranch EXCEPT ![s] = ~onBranch[s]]
        /\ UNCHANGED <<remote, cache, prTask, live, last, rec, sidPr, lost>>)

\* 渡す側が pr:P → task:H/1 を書く・消す。
ToggleTask ==
  Human(/\ prTask' = ~prTask
        /\ UNCHANGED <<remote, cache, live, onBranch, last, rec, sidPr, lost>>)

Next ==
  \/ Tick \/ Merge \/ Forget \/ ToggleTask
  \/ \E s \in S : Hook(s) \/ End(s) \/ Switch(s)

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* 記録が残っている間に P の辺がマージで掃除されるなら、そのとき P は A につながっている。A が終了していても、
\* 別の branch へ移っていても、同じ branch に別のセッションがいても。
IssuerKept == ~lost

\* daemon が導く辺は、P を出したセッションからだけ。
NoWrongAnchor == \A s \in S : sidPr[s] = "derived" => s = Issuer

\* 記録があり、渡す側の辺があり P が open の間に、いずれ A とつながる。
EventuallyIssuer ==
  [](prTask /\ rec /\ remote = "open" /\ (\A s \in S : sidPr[s] = None)
     =><>(sidPr[Issuer] # None \/ ~prTask \/ ~rec \/ remote # "open"))

=============================================================================
