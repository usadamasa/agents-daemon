--------------------------- MODULE SpawnedClose ---------------------------
(* 起こした rex session を `agents-daemon spawned-session close` で畳む手順と、同じ session を閉じうるほかの主体
   (包みの c()、daemon の reaper、利用者) の交錯を写したモデル (#596)。

   - 起こした session (Spawned) は、包みの sh が印を書き (WMark)、claude が終わると (WExit) c() が block を数えて
     自分の pane しか無ければ session を閉じる (WClean)。pane が残っていれば閉じずに抜ける。
   - daemon の reaper は印を回り、rex に無い session の印を消し、pane が無くなった session を閉じて印を消す。
   - 起こしていない session (Others) には印を書かない。利用者はどの session もいつでも閉じられる。
   - rex の server はいつでも落ちうる (Down)。落ちた後の問い合わせは接続の失敗で、target not found とは別の結果。
   - close は対象を 1 つ選び、印を読み (CGetMark)、session を数え (CCount)、居れば閉じ (CKill)、閉じられなければ
     数え直す (CRecount)。各段は rex か state への 1 回の問い合わせで、間に他の主体が割り込める。

   定数で修正前の形を選べる。
   - GoneNeedsMark: 印が無ければ rex を数える前に拒否する (#596 の修正前)。
   - Recount: kill に失敗したら数え直し、無ければ成功にする (#597 で足した)。
   - DownAsNotFound: 接続の失敗を target not found と読む (取り違えの反例を見るための形。実装はこうしない)。
   - IgnoreMark: 印を見ずに rex に居る session を閉じる (NoKillUnmarked が空振りしないことを見るための形)。 *)

CONSTANTS
  Spawned,         \* 包みが起こし、印を書く session
  Others,          \* 起こしていない session (利用者が手で作った session、自分の session)
  GoneNeedsMark,
  Recount,
  DownAsNotFound,
  IgnoreMark

Sessions == Spawned \cup Others
Results == {"none", "ok_killed", "ok_gone", "err"}

VARIABLES
  live,            \* rex にある session
  panes,           \* pane が残っている session (各 session の pane は 1 つに縮める)
  wpc,             \* 包みの段: init / running / exited / done
  marked,          \* 印のある session
  everMarked,      \* 一度でも印を書いた session (性質を書くための記録)
  down,            \* rex の server が落ちたか
  cpc,             \* close の段: idle / mark / count / kill / recount / done
  target,          \* close の対象
  m,               \* close が読んだ印の有無
  goneAtStart,     \* close を打った時点で session が rex に無かったか
  startMarkedLive, \* close を打った時点で印があり rex に居たか
  refused,         \* close が「印の無い生きた session」を見て拒否したか
  result,          \* close の結果
  closeKilled      \* close が閉じた session

vars == <<live, panes, wpc, marked, everMarked, down, cpc, target, m, goneAtStart, startMarkedLive, refused, result,
          closeKilled>>

TypeOK ==
  /\ live \subseteq Sessions
  /\ panes \subseteq Sessions
  /\ wpc \in [Spawned -> {"init", "running", "exited", "done"}]
  /\ marked \subseteq Spawned
  /\ everMarked \subseteq Spawned
  /\ down \in BOOLEAN
  /\ cpc \in {"idle", "mark", "count", "kill", "recount", "done"}
  /\ target \in Sessions \cup {"none"}
  /\ m \in BOOLEAN
  /\ goneAtStart \in BOOLEAN
  /\ startMarkedLive \in BOOLEAN
  /\ refused \in BOOLEAN
  /\ result \in Results
  /\ closeKilled \subseteq Sessions

Init ==
  /\ live = Sessions
  /\ panes = Sessions
  /\ wpc = [s \in Spawned |-> "init"]
  /\ marked = {}
  /\ everMarked = {}
  /\ down = FALSE
  /\ cpc = "idle"
  /\ target = "none"
  /\ m = FALSE
  /\ goneAtStart = FALSE
  /\ startMarkedLive = FALSE
  /\ refused = FALSE
  /\ result = "none"
  /\ closeKilled = {}

CloseVars == <<cpc, target, m, goneAtStart, startMarkedLive, refused, result, closeKilled>>

\* ---- 包み (rexcli の closeOnExitScript) ----

\* `spawned-session mark "$REX_SESSION"` (parkcli の mark → spawnedsession.Put)。
WMark(s) ==
  /\ wpc[s] = "init"
  /\ s \in live
  /\ marked' = marked \cup {s}
  /\ everMarked' = everMarked \cup {s}
  /\ wpc' = [wpc EXCEPT ![s] = "running"]
  /\ UNCHANGED <<live, panes, down>> /\ UNCHANGED CloseVars

\* claude が終わる。
WExit(s) ==
  /\ wpc[s] = "running"
  /\ s \in live
  /\ wpc' = [wpc EXCEPT ![s] = "exited"]
  /\ UNCHANGED <<live, panes, marked, everMarked, down>> /\ UNCHANGED CloseVars

\* c(): block が自分の 1 つだけなら session kill。数えられなければ閉じずに抜ける。抜けると pane が閉じる。
\* c() が閉じても印は消さない (消すのは reaper か close)。
WClean(s) ==
  /\ wpc[s] = "exited"
  /\ wpc' = [wpc EXCEPT ![s] = "done"]
  /\ IF s \in live /\ ~down
       THEN \/ /\ live' = live \ {s}      \* 自分の pane しか無い
               /\ panes' = panes \ {s}
            \/ /\ panes' = panes \ {s}    \* ほかの pane が残っていた (この pane だけ閉じる)
               /\ UNCHANGED live
       ELSE /\ panes' = panes \ {s}
            /\ UNCHANGED live
  /\ UNCHANGED <<marked, everMarked, down>> /\ UNCHANGED CloseVars

\* ---- daemon の reaper (reaper.reapOne) ----

Reap(s) ==
  /\ s \in marked
  /\ ~down
  /\ \/ /\ s \notin live                  \* SessionBlocks が not found: 印だけ消す
        /\ marked' = marked \ {s}
        /\ UNCHANGED <<live, panes>>
     \/ /\ s \in live                     \* pane が無い: 閉じて印を消す
        /\ s \notin panes
        /\ live' = live \ {s}
        /\ marked' = marked \ {s}
        /\ UNCHANGED panes
  /\ UNCHANGED <<wpc, everMarked, down>> /\ UNCHANGED CloseVars

\* ---- 利用者と rex ----

UserClose(s) ==
  /\ s \in live
  /\ live' = live \ {s}
  /\ panes' = panes \ {s}
  /\ UNCHANGED <<wpc, marked, everMarked, down>> /\ UNCHANGED CloseVars

Down ==
  /\ ~down
  /\ down' = TRUE
  /\ UNCHANGED <<live, panes, wpc, marked, everMarked>> /\ UNCHANGED CloseVars

\* rex の SessionBlocks が返す found。接続の失敗は error (DownAsNotFound なら not found と読む)。
Found(s) == ~down /\ s \in live
CountFails == down /\ ~DownAsNotFound

\* ---- close (parkcli の closeSpawnedSession) ----

CloseStart(t) ==
  /\ cpc = "idle"
  /\ target' = t
  /\ goneAtStart' = (t \notin live)
  /\ startMarkedLive' = (t \in marked /\ t \in live)
  /\ cpc' = "mark"
  /\ UNCHANGED <<m, refused, result, closeKilled>>
  /\ UNCHANGED <<live, panes, wpc, marked, everMarked, down>>

\* store.Get。修正前は印が無ければここで拒否する (requireMark)。
CGetMark ==
  /\ cpc = "mark"
  /\ m' = (target \in marked)
  /\ IF GoneNeedsMark /\ target \notin marked
       THEN /\ result' = "err"
            /\ cpc' = "done"
       ELSE /\ cpc' = "count"
            /\ UNCHANGED result
  /\ UNCHANGED <<target, goneAtStart, startMarkedLive, refused, closeKilled>>
  /\ UNCHANGED <<live, panes, wpc, marked, everMarked, down>>

\* closer.SessionBlocks。
CCount ==
  /\ cpc = "count"
  /\ CASE CountFails ->
            /\ result' = "err" /\ cpc' = "done"
            /\ UNCHANGED <<marked, refused>>
       [] ~Found(target) ->
            /\ result' = "ok_gone" /\ cpc' = "done"
            /\ marked' = marked \ {target}
            /\ UNCHANGED refused
       [] ~m /\ ~IgnoreMark ->
            /\ result' = "err" /\ cpc' = "done"
            /\ refused' = TRUE
            /\ UNCHANGED marked
       [] OTHER ->
            /\ cpc' = "kill"
            /\ UNCHANGED <<result, marked, refused>>
  /\ UNCHANGED <<target, m, goneAtStart, startMarkedLive, closeKilled>>
  /\ UNCHANGED <<live, panes, wpc, everMarked, down>>

\* closer.SessionKill (killOrGone の前半)。rex に無い session と落ちた server への kill は失敗する。
CKill ==
  /\ cpc = "kill"
  /\ IF ~down /\ target \in live
       THEN /\ live' = live \ {target}
            /\ panes' = panes \ {target}
            /\ closeKilled' = closeKilled \cup {target}
            /\ marked' = marked \ {target}
            /\ result' = "ok_killed" /\ cpc' = "done"
       ELSE /\ IF Recount
                 THEN /\ cpc' = "recount" /\ UNCHANGED result
                 ELSE /\ result' = "err" /\ cpc' = "done"
            /\ UNCHANGED <<live, panes, closeKilled, marked>>
  /\ UNCHANGED <<target, m, goneAtStart, startMarkedLive, refused>>
  /\ UNCHANGED <<wpc, everMarked, down>>

\* killOrGone の数え直し。無ければ成功、それ以外 (居る、数えられない) は kill の失敗を返す。
CRecount ==
  /\ cpc = "recount"
  /\ IF ~CountFails /\ ~Found(target)
       THEN /\ result' = "ok_gone"
            /\ marked' = marked \ {target}
       ELSE /\ result' = "err"
            /\ UNCHANGED marked
  /\ cpc' = "done"
  /\ UNCHANGED <<target, m, goneAtStart, startMarkedLive, refused, closeKilled>>
  /\ UNCHANGED <<live, panes, wpc, everMarked, down>>

Next ==
  \/ \E s \in Spawned : WMark(s) \/ WExit(s) \/ WClean(s) \/ Reap(s)
  \/ \E s \in Sessions : UserClose(s) \/ CloseStart(s)
  \/ Down
  \/ CGetMark \/ CCount \/ CKill \/ CRecount

\* close の各段は、打った後は他の主体に追い越されても最後まで進む (プロセスは止まらない)。
Fairness == WF_vars(CGetMark) /\ WF_vars(CCount) /\ WF_vars(CKill) /\ WF_vars(CRecount)

Spec == Init /\ [][Next]_vars /\ Fairness

\* ---- 性質 ----

\* 安全性: close は印を書いたことの無い session を閉じない (起こしていない生きた session を巻き込まない)。
NoKillUnmarked == closeKilled \subseteq everMarked

\* 決定性 (#596): 打った時点で畳まれていた session への close は、rex が落ちていなければ必ず ok_gone (exit 0、
\* killed:false) になる。印が残っているか、reaper が先に消したかに依らない。
FoldedIsOk == (cpc = "done" /\ goneAtStart /\ ~down) => result = "ok_gone"

\* FoldedIsOk を、印を書いた後に畳まれた起こした session に絞ったもの (#596 の本筋)。
FoldedSpawnedIsOk == (cpc = "done" /\ goneAtStart /\ target \in everMarked /\ ~down) => result = "ok_gone"

\* close が失敗するのは、rex が落ちたときか、印の無い生きた session を拒否したときだけ。数えた後に他の主体が
\* 閉じた競合を失敗にしない。
ErrOnlyWhenDownOrRefused == result = "err" => (down \/ refused)

\* 成功と返したら、session は本当に rex に無い。接続の失敗を「もう無い」と取り違えない。
OkMeansGone == result \in {"ok_gone", "ok_killed"} => target \notin live

\* 生きている起こした session の印を消さない (reaper が後で拾えなくなる)。
MarkKeptWhileLive == \A s \in Spawned : (s \in everMarked /\ s \in live) => s \in marked

\* liveness: 打った時点で印があり生きていた session は、rex が落ちない限り最終的に無くなる。
MarkedLiveGetsClosed == startMarkedLive ~> (target \notin live \/ down)

\* liveness: close は必ず終わる。
CloseTerminates == (cpc # "idle") ~> (cpc = "done")

=============================================================================
