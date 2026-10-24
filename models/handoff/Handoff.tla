---------------------------- MODULE Handoff ----------------------------
(* パーキングのメモ 1 つを、複数のセッションの mod が渡す・捨てる・adopt する手順を、今の実装のまま写したモデル。

   写しているもの (ファイル:行は 2026-10-24 の main):
   - handOff は「ボードを読む → 送る → park set --status handed」の 3 手で、送った後にだけ handed にする
     (hooks/parking.ts:202-213)。Set は元の status を見ずに書く (internal/parking/parking.go:148-170)。
   - spawn は window を作ってから claude が前景になるのを待ち、待ち切れなければエラーで返す。pane は残り、
     claude が起動していることもある (internal/spawn/spawn.go:131-153)。mod はメモを open のまま残す。
   - board は status.json が今の daemon のものでなければ pane を空として扱い、未完了のタスクを exited と描く
     (internal/board/board.go:195-211、graph.go:149-154)。Adopt はセッションの生死を見ない (parking/place.go:26-28)。
   - Adopt はメモを書いてからタスクの記録を消す 2 手 (place.go:43)。
   - どのファイルの書き込みも statefile.WriteJSONAtomic なので、1 手を原子的なステップにする。

   登場するのはメモ 1 つ、ボードを開いた mod が Mods の数だけ、生きているセッション 1 つ (そのタスクの記録 1 つ)。
   送信と spawn の成否、daemon の生き死には非決定。各 mod がボタンを押す回数は MaxAttempts まで (状態を有限にする)。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Mods,        \* ボードを開いているセッション (mod) の集合
  MaxAttempts  \* 1 つの mod が同じメモを渡そうとする回数の上限

VARIABLES
  memo,        \* メモの status: "open" | "handed" | "discarded"
  view,        \* mod ごとに、最後に読んだボードでのメモの status。"none" は読み直しが要る状態
  pc,          \* mod ごとの手順の位置: "idle" | "delivered" (送った、まだ handed にしていない) | "adopting" (メモを書いた、まだ記録を消していない)
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
  /\ pc \in [Mods -> {"idle", "delivered", "adopting"}]
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

(* 渡す (peer / self)。mod は自分が読んだボードで open だったメモを渡す。届けば delivered へ進み、届かなければ
   メモを open のまま残して読み直しに戻る (parking.ts:202-209)。 *)
Send(m) ==
  /\ pc[m] = "idle" /\ view[m] = "open" /\ attempts[m] < MaxAttempts
  /\ attempts' = [attempts EXCEPT ![m] = @ + 1]
  /\ \/ /\ deliveries' = deliveries + 1
        /\ pc' = [pc EXCEPT ![m] = "delivered"]
        /\ UNCHANGED view
     \/ /\ view' = [view EXCEPT ![m] = "none"]
        /\ UNCHANGED <<deliveries, pc>>
  /\ UNCHANGED <<memo, sessions, daemonFresh, task, taskMemo, adoptedLive>>

(* 新しいセッションを起こして渡す。入力の検査で拒まれれば何も起きない。window を作れば claude が前景になるのを待ち、
   なれば届いたことになる。待ち切れなければ pane は残り claude が起動していることもあるので、セッションは数えたうえで
   届かなかったとして open のまま残す (spawn.go:145、parking.ts:206-209)。 *)
Spawn(m) ==
  /\ pc[m] = "idle" /\ view[m] = "open" /\ attempts[m] < MaxAttempts
  /\ attempts' = [attempts EXCEPT ![m] = @ + 1]
  /\ \/ /\ view' = [view EXCEPT ![m] = "none"]                     \* check で拒否
        /\ UNCHANGED <<deliveries, sessions, pc>>
     \/ /\ sessions' = sessions + 1 /\ deliveries' = deliveries + 1 \* 前景になった
        /\ pc' = [pc EXCEPT ![m] = "delivered"]
        /\ UNCHANGED view
     \/ /\ sessions' = sessions + 1                                 \* 待ち切れ。claude は起きている
        /\ view' = [view EXCEPT ![m] = "none"]
        /\ UNCHANGED <<deliveries, pc>>
  /\ UNCHANGED <<memo, daemonFresh, task, taskMemo, adoptedLive>>

(* 届いた後の park set --status handed。元の status を見ずに書く。失敗すれば open のまま残る (parking.ts:211-212)。 *)
SetHanded(m) ==
  /\ pc[m] = "delivered"
  /\ pc' = [pc EXCEPT ![m] = "idle"]
  /\ view' = [view EXCEPT ![m] = "none"]
  /\ \/ memo' = "handed"
     \/ UNCHANGED memo
  /\ UNCHANGED <<attempts, deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

(* ボードの Discard。読んだボードで open だったメモを discarded にする (parking.ts:183-186)。 *)
Discard(m) ==
  /\ pc[m] = "idle" /\ view[m] = "open"
  /\ memo' = "discarded"
  /\ view' = [view EXCEPT ![m] = "none"]
  /\ UNCHANGED <<pc, attempts, deliveries, sessions, daemonFresh, task, taskMemo, adoptedLive>>

-----------------------------------------------------------------------------
(* daemon が止まる・入れ替わる・起きる。止まっている間と、前の daemon が残した status.json しか無い間は、
   board は pane を空として扱う (board.go:195-211)。 *)
DaemonStop == /\ daemonFresh /\ daemonFresh' = FALSE
              /\ UNCHANGED <<memo, view, pc, attempts, deliveries, sessions, task, taskMemo, adoptedLive>>
DaemonStart == /\ ~daemonFresh /\ daemonFresh' = TRUE
               /\ UNCHANGED <<memo, view, pc, attempts, deliveries, sessions, task, taskMemo, adoptedLive>>

(* ボードの To parking。pane が無いので exited と描かれたカードに出る (graph.go:153-154、parking.ts:143-145)。
   生きているセッションの pane が無く見えるのは daemon の一覧が信用できないとき。Adopt は生死を見ずに
   メモを書く (place.go:29-39)。 *)
AdoptWrite(m) ==
  /\ pc[m] = "idle" /\ task /\ ~daemonFresh
  /\ taskMemo' = TRUE
  /\ pc' = [pc EXCEPT ![m] = "adopting"]
  /\ UNCHANGED <<memo, view, attempts, deliveries, sessions, daemonFresh, task, adoptedLive>>

(* Adopt の 2 手目。タスクの記録を消す。消し損ねればメモだけが残る (place.go:43-44)。 *)
AdoptRemove(m) ==
  /\ pc[m] = "adopting"
  /\ pc' = [pc EXCEPT ![m] = "idle"]
  /\ \/ /\ task' = FALSE /\ adoptedLive' = TRUE
     \/ UNCHANGED <<task, adoptedLive>>
  /\ UNCHANGED <<memo, view, attempts, deliveries, sessions, daemonFresh, taskMemo>>

-----------------------------------------------------------------------------
Next ==
  \/ \E m \in Mods : Read(m) \/ Send(m) \/ Spawn(m) \/ SetHanded(m) \/ Discard(m) \/ AdoptWrite(m) \/ AdoptRemove(m)
  \/ DaemonStop \/ DaemonStart

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* 不変条件。#242 の表の順。1〜3 は今の設計で破れる (TLC が反例を出す)。4 (表の 5 行目) は許容の形で成り立つ。 *)

\* 1. 1 つのメモは高々 1 つの宛先に渡る
AtMostOneDelivery == deliveries <= 1

\* 2. 生きているセッションのタスクの記録は adopt で消えない
LiveTaskKept == ~adoptedLive

\* 3. 1 つのメモから起きるセッションは高々 1 つ
AtMostOneSession == sessions <= 1

\* 4. adopt でメモかタスクの少なくとも一方は残る
AdoptLeavesOne == task \/ taskMemo

=============================================================================
