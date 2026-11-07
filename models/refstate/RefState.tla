---------------------------- MODULE RefState ----------------------------
(* 辺の端点の外部の参照 (issue:<owner>/<repo>#<番号> / pr:<owner>/<repo>#<番号>) 1 つの状態を、daemon が gh で引いて
   refs/<id>.json に写し、依存図 (deps の graph) がそれを読んでノードの状態を決める手順を写したモデル (#416)。

   - GitHub の側の状態 remote は open から merged か closed へ 1 回だけ移る。closed の issue を開き直す遷移は置かない
     (daemon は merged / closed を引き直さないので、開き直しは写しの保持期限が切れるまで反映されない。README の前提)。
   - daemon の引き直し (daemon の refpoll) は gh を呼ぶステップと、結果を写しへ書くステップに分ける。間に remote が
     動いても、書くのは呼んだときの値。
   - gh は失敗しうる (無い、未認証、rate limit、network)。失敗したら写しを書かない。FailAsClosed が TRUE の変種は
     失敗を closed として書く (性質を破る壊れたモデル)。
   - 写しが merged / closed なら引き直さない。写しが無いか open なら引き直す。間引き (前回から一定の間隔) は時間を
     置かずに公平性で表す。RefetchOpen が FALSE の変種は写しが open なら引き直さない (性質を破る壊れたモデル)。
   - 写しは daemon の housekeeping が保持期間で消しうる (Prune)。消えたら次の引き直しで書き直す。
   - 写しの書き込みは statefile.WriteJSONAtomic で、書くのは daemon の 1 つの goroutine だけなので 1 ステップ。 *)
EXTENDS Naturals

CONSTANTS
  FailAsClosed,  \* gh の失敗を closed として写しに書くか (壊れた変種)
  RefetchOpen    \* 写しが open の参照を引き直すか

None == "none"
Done == {"merged", "closed"}
States == {"open"} \cup Done

VARIABLES
  remote,  \* GitHub の側の状態
  cache,   \* refs/<id>.json の写し。無ければ None
  pc,      \* 引き直しの段: idle / called
  got      \* gh の結果。失敗なら "fail"

vars == <<remote, cache, pc, got>>

TypeOK ==
  /\ remote \in States
  /\ cache \in {None} \cup States
  /\ pc \in {"idle", "called"}
  /\ got \in {None, "fail"} \cup States

\* 辺を書いた時点の参照は open。daemon はまだ引いていない。
Init ==
  /\ remote = "open"
  /\ cache = None
  /\ pc = "idle"
  /\ got = None

\* PR がマージされる、issue / PR が close される。
Finish(s) ==
  /\ remote = "open"
  /\ s \in Done
  /\ remote' = s
  /\ UNCHANGED <<cache, pc, got>>

\* 引き直す参照か。写しが merged / closed なら引かない。
Due == cache = None \/ (RefetchOpen /\ cache = "open")

\* gh を呼び、今の remote を受け取る。
CallOK ==
  /\ pc = "idle"
  /\ Due
  /\ got' = remote
  /\ pc' = "called"
  /\ UNCHANGED <<remote, cache>>

\* gh を呼んで失敗する。
CallFail ==
  /\ pc = "idle"
  /\ Due
  /\ got' = "fail"
  /\ pc' = "called"
  /\ UNCHANGED <<remote, cache>>

\* 結果を写しへ書く。失敗なら書かない (壊れた変種は closed と書く)。
Store ==
  /\ pc = "called"
  /\ cache' = CASE got \in States -> got
               [] FailAsClosed -> "closed"
               [] OTHER -> cache
  /\ pc' = "idle"
  /\ got' = None
  /\ UNCHANGED remote

\* housekeeping が保持期間を過ぎた写しを消す。
Prune ==
  /\ cache # None
  /\ cache' = None
  /\ UNCHANGED <<remote, pc, got>>

\* 依存図がノードに付ける状態。
Shown == IF cache \in Done THEN "completed" ELSE "external"

Next ==
  \/ \E s \in Done : Finish(s)
  \/ CallOK \/ CallFail \/ Store
  \/ Prune

\* gh はいずれ成功する (無い・未認証のまま直されない場合は liveness の前提の外。README の前提)。
\* 写しを消すのは保持期間ごとで、引き直しより十分まれなので、消し続けて引き直しを阻むことは公平性に含めない。
Spec ==
  /\ Init /\ [][Next]_vars
  /\ SF_vars(CallOK)
  /\ WF_vars(Store)

\* 済んでいない参照を完了と見せない (gh の失敗で completed に倒さない)。
NoFalseComplete == Shown = "completed" => remote \in Done

\* 写しが merged / closed なら、GitHub の側も同じ状態 (済んだ状態は戻らないので、引いた値はそのまま正しい)。
CacheSound == cache \in Done => cache = remote

\* 済んだ参照は、いずれ依存図で完了になる。
EventuallyCompleted == (remote \in Done) ~> (Shown = "completed")

=============================================================================
