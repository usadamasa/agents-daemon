---------------------------- MODULE RefState ----------------------------
(* 辺の端点の外部の参照 (issue:<owner>/<repo>#<番号> / pr:<owner>/<repo>#<番号>) 1 つの状態を、daemon が gh で引いて
   refs/<id>.json に写し、依存図 (deps の graph) がそれを読んでノードの状態を決める手順を写したモデル (#416)。

   - GitHub の側の状態 remote は open から、済んだ状態 (merged の PR、completed で閉じた issue = closed) か、
     取りやめ (マージせずに閉じた PR、not planned / duplicate で閉じた issue = dropped) へ 1 回だけ移る。開き直す遷移は
     置かない (daemon は閉じた参照を引き直さないので、開き直しは写しの保持期限が切れるまで反映されない。README の前提)。
   - daemon の引き直し (daemon の refpoll) は gh を呼ぶステップと、結果を写しへ書くステップに分ける。間に remote が
     動いても、書くのは呼んだときの値。
   - gh は失敗しうる (無い、未認証、rate limit、network)。失敗したら写しを書かない。FailAsClosed が TRUE の変種は
     失敗を closed として書く (性質を破る壊れたモデル)。
   - DroppedAsDone が TRUE の変種は、取りやめを済んだ状態 (closed) として書く。gh の state (CLOSED) だけを見て、
     マージしたか・閉じた理由を見ない形 (性質を破る壊れたモデル)。
   - 写しが閉じた状態 (merged / closed / dropped) なら引き直さない。写しが無いか open なら引き直す。間引き (前回から
     一定の間隔) は時間を置かずに公平性で表す。RefetchOpen が FALSE の変種は写しが open なら引き直さない
     (性質を破る壊れたモデル)。
   - 写しは daemon の housekeeping が保持期間で消しうる (Prune)。消えたら次の引き直しで書き直す。
   - 写しの書き込みは statefile.WriteJSONAtomic で、書くのは daemon の 1 つの goroutine だけなので 1 ステップ。
   - この参照を before に持つ辺 1 本 (edges/<id>.json、#422)。渡す側はいつでも `edge add` で書きうる (AddEdge)。
     daemon は引き直しのたびに、写しが済んだ状態の参照を before に持つ辺を消す (Sweep、refpoll の sweep)。取りやめの
     参照の辺は残す (待っていたものは届いていないので、人が付け替えるか `edge rm` する)。
     SweepOnTransition が TRUE の変種は、写しが済んだ状態に変わった引き直しのときだけ消す (性質を破る壊れたモデル)。
     辺の before は書き換わらない (結びつけるのは session: の端点だけ) ので、消すかは写しだけで決まる。 *)
EXTENDS Naturals

CONSTANTS
  FailAsClosed,       \* gh の失敗を closed として写しに書くか (壊れた変種)
  DroppedAsDone,      \* 取りやめを closed として写しに書くか (壊れた変種)
  RefetchOpen,        \* 写しが open の参照を引き直すか
  SweepOnTransition,  \* 辺を写しが済んだ状態に変わったときだけ消すか (壊れた変種)
  MaxAdds             \* `edge add` を走らせる回数

None == "none"
Done == {"merged", "closed"}
Dropped == "dropped"
Final == Done \cup {Dropped}
States == {"open"} \cup Final

VARIABLES
  remote,   \* GitHub の側の状態
  cache,    \* refs/<id>.json の写し。無ければ None
  pc,       \* 引き直しの段: idle / called
  got,      \* gh の結果。失敗なら "fail"
  edge,     \* この参照を before に持つ辺: present / none
  adds,     \* 走らせた `edge add` の数
  wrongly   \* daemon が GitHub の側で済んでいない参照の辺を消したことがあるか (履歴)

vars == <<remote, cache, pc, got, edge, adds, wrongly>>

TypeOK ==
  /\ remote \in States
  /\ cache \in {None} \cup States
  /\ pc \in {"idle", "called"}
  /\ got \in {None, "fail"} \cup States
  /\ edge \in {"present", "none"}
  /\ adds \in 0..MaxAdds
  /\ wrongly \in BOOLEAN

\* 辺を書いた時点の参照は open。daemon はまだ引いていない。
Init ==
  /\ remote = "open"
  /\ cache = None
  /\ pc = "idle"
  /\ got = None
  /\ edge = "present"
  /\ adds = 0
  /\ wrongly = FALSE

\* daemon が辺を消す。消した時点の GitHub の側の状態を履歴に残す。
RemoveEdge ==
  /\ edge' = "none"
  /\ wrongly' = (wrongly \/ remote \notin Done)

\* PR がマージされる・マージせずに閉じられる、issue が completed / not planned で閉じられる。
Finish(s) ==
  /\ remote = "open"
  /\ s \in Final
  /\ remote' = s
  /\ UNCHANGED <<cache, pc, got, edge, adds, wrongly>>

\* 渡す側が `edge add` で辺を書く (消された後に書き直す、済んだ PR へ後から書く、を含む)。
AddEdge ==
  /\ edge = "none"
  /\ adds < MaxAdds
  /\ edge' = "present"
  /\ adds' = adds + 1
  /\ UNCHANGED <<remote, cache, pc, got, wrongly>>

\* 写しが済んだ状態なら辺を消す (引き直しのたびに掃く)。取りやめは消さない。
Sweep ==
  /\ ~SweepOnTransition
  /\ edge = "present"
  /\ cache \in Done
  /\ RemoveEdge
  /\ UNCHANGED <<remote, cache, pc, got, adds>>

\* 引き直す参照か。写しが閉じた状態なら引かない。
Due == cache = None \/ (RefetchOpen /\ cache = "open")

\* gh を呼び、今の remote を受け取る。
CallOK ==
  /\ pc = "idle"
  /\ Due
  /\ got' = remote
  /\ pc' = "called"
  /\ UNCHANGED <<remote, cache, edge, adds, wrongly>>

\* gh を呼んで失敗する。
CallFail ==
  /\ pc = "idle"
  /\ Due
  /\ got' = "fail"
  /\ pc' = "called"
  /\ UNCHANGED <<remote, cache, edge, adds, wrongly>>

\* 結果を写しへ書く。失敗なら書かない (壊れた変種は closed と書く)。
\* SweepOnTransition の変種は、写しが済んだ状態に変わったこのときだけ辺を消す。
Store ==
  /\ pc = "called"
  /\ cache' = CASE got = Dropped /\ DroppedAsDone -> "closed"
               [] got \in States -> got
               [] FailAsClosed -> "closed"
               [] OTHER -> cache
  /\ pc' = "idle"
  /\ got' = None
  /\ IF SweepOnTransition /\ edge = "present" /\ cache' \in Done /\ cache \notin Done
       THEN RemoveEdge
       ELSE UNCHANGED <<edge, wrongly>>
  /\ UNCHANGED <<remote, adds>>

\* housekeeping が保持期間を過ぎた写しを消す。
Prune ==
  /\ cache # None
  /\ cache' = None
  /\ UNCHANGED <<remote, pc, got, edge, adds, wrongly>>

\* 依存図がノードに付ける状態。
Shown == CASE cache \in Done -> "completed"
           [] cache = Dropped -> "dropped"
           [] cache = "open" -> "waiting"
           [] OTHER -> "external"

Next ==
  \/ \E s \in Final : Finish(s)
  \/ CallOK \/ CallFail \/ Store
  \/ Prune
  \/ AddEdge \/ Sweep

\* gh はいずれ成功する (無い・未認証のまま直されない場合は liveness の前提の外。README の前提)。
\* 写しを消すのは保持期間ごとで、引き直しより十分まれなので、消し続けて引き直しを阻むことは公平性に含めない。
\* 掃くのは tick ごとの引き直しで、写しが済んだ状態の間は tick ごとに試みる。写しの削除と書き直しが
\* 交互に続いても、写しが済んだ状態の間に tick が来るので強い公平性にする。
Spec ==
  /\ Init /\ [][Next]_vars
  /\ SF_vars(CallOK)
  /\ WF_vars(Store)
  /\ SF_vars(Sweep)

\* 済んでいない参照 (open、取りやめ) を完了と見せない (gh の失敗や、マージせずに閉じたことで completed に倒さない)。
NoFalseComplete == Shown = "completed" => remote \in Done

\* 写しが閉じた状態なら、GitHub の側も同じ状態 (閉じた状態は戻らないので、引いた値はそのまま正しい)。
CacheSound == cache \in Final => cache = remote

\* 済んだ参照は、いずれ依存図で完了になる。
EventuallyCompleted == (remote \in Done) ~> (Shown = "completed")

\* 取りやめの参照は、いずれ依存図で取りやめになる。
EventuallyDropped == (remote = Dropped) ~> (Shown = "dropped")

\* GitHub の側で済んでいない (open、取りやめ) 参照を before に持つ辺を daemon が消さない。
OnlyDoneRemoved == ~wrongly

\* 済んだ参照を before に持つ辺は、いずれ消える (済んだ後に書いた辺も)。
EdgeRemoved == (edge = "present" /\ remote \in Done) ~> (edge = "none")

=============================================================================
