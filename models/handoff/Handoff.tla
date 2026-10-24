---------------------------- MODULE Handoff ----------------------------
(* パーキングのメモ 1 つを、複数のセッションの mod が渡す・捨てる・adopt する手順のモデル。

   写しているもの:
   - handOff は「ボードを読む → 取る (park set --status handed --from open) → 送る → 届かなければ戻す
     (park set --status open --from handed)」(hooks/parking.ts の handOff)。--from は元の status が違えば失敗する
     (internal/parking の Store.Set)。取れるのは 1 つの mod だけで、取られたメモはほかの mod のボードに open と出ても取れない。
   - spawn は window を作ってから claude が前景になるのを待ち、待ち切れなければエラーで返す。pane は残り、
     claude が起動していることもある (internal/spawn/spawn.go の waitForClaude)。mod は届かなかったとして戻す。
   - board は status.json が今の daemon のものでなければ pane を空として扱い、未完了のタスクを exited と描く
     (internal/board/board.go の livePanes、graph.go)。Adopt はセッションの生死を見ない (parking/place.go の Adopt)。
   - Adopt はメモを書いてからタスクの記録を消す 2 手 (place.go)。
   - どのファイルの書き込みも statefile.WriteJSONAtomic なので、1 手を原子的なステップにする。

   登場するのはメモ 1 つ、ボードを開いた mod が Mods の数だけ、生きているセッション 1 つ (そのタスクの記録 1 つ)。
   送信と spawn の成否、park set の成否、daemon の生き死には非決定。各 mod がボタンを押す回数は MaxAttempts まで
   (状態を有限にする)。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Mods,        \* ボードを開いているセッション (mod) の集合
  MaxAttempts  \* 1 つの mod が同じメモを渡そうとする回数の上限

VARIABLES
  memo,        \* メモの status: "open" | "handed" | "discarded"
  view,        \* mod ごとに、最後に読んだボードでのメモの status。"none" は読み直しが要る状態
  pc,          \* mod ごとの手順の位置: "idle" | "claimed" (取った、まだ送っていない) | "undelivered" (送ったが届かなかった、まだ戻していない) | "adopting" (メモを書いた、まだ記録を消していない)
  attempts,    \* mod ごとの、渡そうとした回数
  deliveries,  \* このメモが宛先に届いた回数 (peer / self / spawn を問わない)
  sessions,    \* このメモから起きたセッションの数
  daemonFresh, \* status.json が今の daemon のもの (daemon が動いていて、pane の一覧が信用できる)
  task,        \* 生きているセッションのタスクの記録が残っているか
  taskMemo,    \* adopt がそのタスクから作ったメモがあるか
  adoptedLive  \* 生きているセッションのタスクの記録を adopt で消したか

vars == <<memo, view, pc, attempts, deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

Status == {"open", "handed", "discarded"}

TypeOK ==
  /\ memo \in Status
  /\ view \in [Mods -> Status \cup {"none"}]
  /\ pc \in [Mods -> {"idle", "claimed", "undelivered", "adopting"}]
  /\ attempts \in [Mods -> 0..MaxAttempts]
  /\ deliveries \in Nat
  /\ sessions \in Nat
  /\ daemonFresh \in BOOLEAN
  /\ task \in BOOLEAN
  /\ taskMemo \in BOOLEAN
  /\ adoptedLive \in BOOLEAN

Init ==
  /\ memo = "open"
  /\ view = [m \in Mods |-> "none"]
  /\ pc = [m \in Mods |-> "idle"]
  /\ attempts = [m \in Mods |-> 0]
  /\ deliveries = 0
  /\ sessions = 0
  /\ daemonFresh = TRUE
  /\ task = TRUE
  /\ taskMemo = FALSE
  /\ adoptedLive = FALSE

-----------------------------------------------------------------------------
(* ボードを読み直す。status --board がファイルを読んで JSON にしたものを mod が描く。 *)
Read(m) ==
  /\ pc[m] = "idle"
  /\ view' = [view EXCEPT ![m] = memo]
  /\ UNCHANGED <<memo, pc, attempts, deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

(* 取る。mod は自分が読んだボードで open だったメモを、送る前に park set --status handed --from open で取る。
   ファイルの status が open でなければ (ほかの mod が先に取った、捨てた) 失敗し、読み直しに戻る。
   プロセスの失敗も同じ経路 (parking.ts の handOff)。 *)
Claim(m) ==
  /\ pc[m] = "idle" /\ view[m] = "open" /\ attempts[m] < MaxAttempts
  /\ attempts' = [attempts EXCEPT ![m] = @ + 1]
  /\ \/ /\ memo = "open"
        /\ memo' = "handed"
        /\ pc' = [pc EXCEPT ![m] = "claimed"]
        /\ UNCHANGED view
     \/ /\ view' = [view EXCEPT ![m] = "none"]
        /\ UNCHANGED <<memo, pc>>
  /\ UNCHANGED <<deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

(* 取ったメモを渡す (peer / self)。届けば handed のまま終わり、届かなければ戻しに進む。 *)
Send(m) ==
  /\ pc[m] = "claimed"
  /\ \/ /\ deliveries' = deliveries + 1
        /\ pc' = [pc EXCEPT ![m] = "idle"]
        /\ view' = [view EXCEPT ![m] = "none"]
     \/ /\ pc' = [pc EXCEPT ![m] = "undelivered"]
        /\ UNCHANGED <<deliveries, view>>
  /\ UNCHANGED <<memo, attempts, sessions, daemonFresh, task, taskMemo, adoptedLive>>

(* 取ったメモを、新しいセッションを起こして渡す。入力の検査で拒まれれば何も起きず戻しに進む。window を作れば claude が
   前景になるのを待ち、なれば届いたことになる。待ち切れなければ pane は残り claude が起動していることもあるので、
   セッションは数えたうえで届かなかったとして戻しに進む (spawn.go の waitForClaude、parking.ts の handOff)。 *)
Spawn(m) ==
  /\ pc[m] = "claimed"
  /\ \/ /\ pc' = [pc EXCEPT ![m] = "undelivered"]                   \* check で拒否
        /\ UNCHANGED <<deliveries, sessions, view>>
     \/ /\ sessions' = sessions + 1 /\ deliveries' = deliveries + 1 \* 前景になった
        /\ pc' = [pc EXCEPT ![m] = "idle"]
        /\ view' = [view EXCEPT ![m] = "none"]
     \/ /\ sessions' = sessions + 1                                 \* 待ち切れ。claude は起きている
        /\ pc' = [pc EXCEPT ![m] = "undelivered"]
        /\ UNCHANGED <<deliveries, view>>
  /\ UNCHANGED <<memo, attempts, daemonFresh, task, taskMemo, adoptedLive>>

(* 届かなかったメモを park set --status open --from handed で戻す。プロセスが失敗すれば handed のまま残る
   (人が park set で戻す)。 *)
Revert(m) ==
  /\ pc[m] = "undelivered"
  /\ pc' = [pc EXCEPT ![m] = "idle"]
  /\ view' = [view EXCEPT ![m] = "none"]
  /\ \/ memo' = "open"
     \/ UNCHANGED memo
  /\ UNCHANGED <<attempts, deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

(* ボードの Discard。読んだボードで open だったメモを park set --status discarded --from open で捨てる。
   先に取られていれば失敗する。 *)
Discard(m) ==
  /\ pc[m] = "idle" /\ view[m] = "open"
  /\ view' = [view EXCEPT ![m] = "none"]
  /\ \/ /\ memo = "open" /\ memo' = "discarded"
     \/ UNCHANGED memo
  /\ UNCHANGED <<pc, attempts, deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

-----------------------------------------------------------------------------
(* daemon が止まる・入れ替わる・起きる。止まっている間と、前の daemon が残した status.json しか無い間は、
   board は pane を空として扱う (board.go の livePanes)。 *)
DaemonStop == /\ daemonFresh /\ daemonFresh' = FALSE
              /\ UNCHANGED <<memo, view, pc, attempts, deliveries, sessions, task, taskMemo, adoptedLive>>
DaemonStart == /\ ~daemonFresh /\ daemonFresh' = TRUE
               /\ UNCHANGED <<memo, view, pc, attempts, deliveries, sessions, task, taskMemo, adoptedLive>>

(* ボードの To parking。pane が無いので exited と描かれたカードに出る (graph.go、parking.ts の canAdopt)。
   生きているセッションの pane が無く見えるのは daemon の一覧が信用できないとき。Adopt は生死を見ずに
   メモを書く (place.go の Adopt)。 *)
AdoptWrite(m) ==
  /\ pc[m] = "idle" /\ task /\ ~daemonFresh
  /\ taskMemo' = TRUE
  /\ pc' = [pc EXCEPT ![m] = "adopting"]
  /\ UNCHANGED <<memo, view, attempts, deliveries, sessions, daemonFresh, task, adoptedLive>>

(* Adopt の 2 手目。タスクの記録を消す。消し損ねればメモだけが残る (place.go の Adopt)。 *)
AdoptRemove(m) ==
  /\ pc[m] = "adopting"
  /\ pc' = [pc EXCEPT ![m] = "idle"]
  /\ \/ /\ task' = FALSE /\ adoptedLive' = TRUE
     \/ UNCHANGED <<task, adoptedLive>>
  /\ UNCHANGED <<memo, view, attempts, deliveries, sessions, daemonFresh, taskMemo>>

-----------------------------------------------------------------------------
Next ==
  \/ \E m \in Mods : Read(m) \/ Claim(m) \/ Send(m) \/ Spawn(m) \/ Revert(m) \/ Discard(m) \/ AdoptWrite(m) \/ AdoptRemove(m)
  \/ DaemonStop \/ DaemonStart

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* 不変条件。#242 の表の順。 *)

\* 1. 1 つのメモは高々 1 つの宛先に渡る
AtMostOneDelivery == deliveries <= 1

\* 2. 生きているセッションのタスクの記録は adopt で消えない
LiveTaskKept == ~adoptedLive

\* 3. 1 つのメモから起きるセッションは高々 1 つ
AtMostOneSession == sessions <= 1

\* 4. adopt でメモかタスクの少なくとも一方は残る
AdoptLeavesOne == task \/ taskMemo

\* 5. 届いたメモは handed のまま (捨てる操作が後から上書きしない)
HandedWhenDelivered == deliveries > 0 => memo = "handed"

=============================================================================
