---------------------------- MODULE Handoff ----------------------------
(* パーキングのメモ 1 つを、複数のセッションの mod が渡す・捨てる・adopt する手順のモデル。

   写しているもの:
   - handOff は「依存図を読む → 取る (park set の status handed、from open) → 送る → 届かなければ戻す
     (park set の status open、from handed)」(hooks/parking.ts の handOff)。from は元の status が違えば失敗する
     (parking の Store.Set)。取れるのは 1 つの mod だけで、取られたメモはほかの mod の依存図に open と出ても取れない。
   - spawn は window を作ってから claude が前景になるのを待ち、待ち切れなければ foreground=false で返す。pane は残り、
     claude が起動していることもある (spawn/spawn.go の waitForClaude)。mod はそのメモを戻さず handed のまま残す。
   - deps は status.json が今の daemon のものでなければ pane を空として扱い、未完了のタスクを exited と描く
     (deps/deps.go の livePanes、graph.go)。Adopt は「今の daemon がタスクの記録より後に書いた status.json に
     そのセッションの pane が無い」ときだけ通す (daemonstate の SessionExited、parking/place.go の Adopt)。
   - Adopt はメモを書いてからタスクの記録を消す 2 手 (place.go)。
   - 取るときは宛先 (peer と self は sid:<session_id>、spawn は起こす先の repo:<path>) を同じ park set の to_session / to_repo で
     メモの handed_to に書く。戻すと handed_to を空にする (parking の Store.Set)。SplitClaim が TRUE なら、
     取るのと宛先を書くのを別の park set にした形を写す (宛先の無い handed が見える)。
   - どのファイルの書き込みも statefile.WriteJSONAtomic なので、1 手を原子的なステップにする。

   登場するのはメモ 1 つ、依存図を開いた mod が Mods の数だけ、セッション 1 つ (そのタスクの記録 1 つ)。セッションは
   途中で終了しうる。送信と spawn の成否、park set の成否、daemon の生き死には非決定。各 mod がボタンを押す回数は
   MaxAttempts まで (状態を有限にする)。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Mods,        \* 依存図を開いているセッション (mod) の集合
  MaxAttempts, \* 1 つの mod が同じメモを渡そうとする回数の上限
  SplitClaim   \* 取るのと宛先を書くのを別の park set にするか

\* 宛先。peer と self は sid:<session_id>、spawn は repo:<path>。
Dests == {"peer", "self", "repo"}
None == "none"

VARIABLES
  memo,        \* メモの status: "open" | "handed" | "discarded"
  to,          \* メモの handed_to (宛先)。無ければ None
  dest,        \* mod ごとの、取ろうとしている宛先
  deliveredTo, \* メモが届いた宛先。届いていなければ None
  view,        \* mod ごとに、最後に読んだ依存図でのメモの status。"none" は読み直しが要る状態
  pc,          \* mod ごとの手順の位置: "idle" | "recording" (SplitClaim で取った、まだ宛先を書いていない) | "claimed" (取った、まだ送っていない) | "undelivered" (送ったが届かなかった、まだ戻していない) | "adopting" (メモを書いた、まだ記録を消していない)
  attempts,    \* mod ごとの、渡そうとした回数
  deliveries,  \* このメモが宛先に届いた回数 (peer / self / spawn を問わない)
  sessions,    \* このメモから起きたセッションの数
  live,        \* タスクの記録を持つセッションが生きているか
  seen,        \* 今の daemon の status.json がそのセッションをどう見ているか: "live" | "exited" | "none" (daemon が止まっている、前の daemon のもの)
  task,        \* そのセッションのタスクの記録が残っているか
  taskMemo,    \* adopt がそのタスクから作ったメモがあるか
  adoptedLive  \* 生きているセッションのタスクの記録を adopt で消したか

handoff == <<to, dest, deliveredTo>>
vars == <<memo, to, dest, deliveredTo, view, pc, attempts, deliveries, sessions, live, seen, task, taskMemo, adoptedLive>>

Status == {"open", "handed", "discarded"}

TypeOK ==
  /\ memo \in Status
  /\ to \in Dests \cup {None}
  /\ dest \in [Mods -> Dests \cup {None}]
  /\ deliveredTo \in Dests \cup {None}
  /\ view \in [Mods -> Status \cup {"none"}]
  /\ pc \in [Mods -> {"idle", "recording", "claimed", "undelivered", "adopting"}]
  /\ attempts \in [Mods -> 0..MaxAttempts]
  /\ deliveries \in Nat
  /\ sessions \in Nat
  /\ live \in BOOLEAN
  /\ seen \in {"live", "exited", "none"}
  /\ task \in BOOLEAN
  /\ taskMemo \in BOOLEAN
  /\ adoptedLive \in BOOLEAN

(* 始まりは、生きているセッションの記録を daemon が見ている状態。記録より古い status.json は Adopt が受け付けないので、
   「記録はあるのに daemon がまだそのセッションを見ていない」状態は始まりに置かない。 *)
Init ==
  /\ memo = "open"
  /\ to = None
  /\ dest = [m \in Mods |-> None]
  /\ deliveredTo = None
  /\ view = [m \in Mods |-> "none"]
  /\ pc = [m \in Mods |-> "idle"]
  /\ attempts = [m \in Mods |-> 0]
  /\ deliveries = 0
  /\ sessions = 0
  /\ live = TRUE
  /\ seen = "live"
  /\ task = TRUE
  /\ taskMemo = FALSE
  /\ adoptedLive = FALSE

-----------------------------------------------------------------------------
(* 依存図を読み直す。agents-daemon deps がファイルを読んで JSON にしたものを mod が描く。 *)
Read(m) ==
  /\ pc[m] = "idle"
  /\ view' = [view EXCEPT ![m] = memo]
  /\ UNCHANGED <<memo, handoff, pc, attempts, deliveries, sessions, live, seen, task, taskMemo, adoptedLive>>

(* 取る。mod は自分が読んだ依存図で open だったメモを、送る前に park set (status handed、from open、宛先) で取る。
   ファイルの status が open でなければ (ほかの mod が先に取った、捨てた) 失敗し、読み直しに戻る。
   プロセスの失敗も同じ経路 (parking.ts の handOff)。SplitClaim なら宛先は次の手で書く。 *)
Claim(m, d) ==
  /\ pc[m] = "idle" /\ view[m] = "open" /\ attempts[m] < MaxAttempts
  /\ attempts' = [attempts EXCEPT ![m] = @ + 1]
  /\ dest' = [dest EXCEPT ![m] = d]
  /\ \/ /\ memo = "open"
        /\ memo' = "handed"
        /\ to' = IF SplitClaim THEN None ELSE d
        /\ pc' = [pc EXCEPT ![m] = IF SplitClaim THEN "recording" ELSE "claimed"]
        /\ UNCHANGED view
     \/ /\ view' = [view EXCEPT ![m] = "none"]
        /\ UNCHANGED <<memo, to, pc>>
  /\ UNCHANGED <<deliveredTo, deliveries, sessions, live, seen, task, taskMemo, adoptedLive>>

(* SplitClaim: 取った後の別の park set で宛先を書く。 *)
Record(m) ==
  /\ pc[m] = "recording"
  /\ to' = dest[m]
  /\ pc' = [pc EXCEPT ![m] = "claimed"]
  /\ UNCHANGED <<memo, dest, deliveredTo, view, attempts, deliveries, sessions, live, seen, task, taskMemo, adoptedLive>>

(* 取ったメモを渡す (peer / self)。届けば handed のまま終わり、届かなければ戻しに進む。 *)
Send(m) ==
  /\ pc[m] = "claimed" /\ dest[m] \in {"peer", "self"}
  /\ \/ /\ deliveries' = deliveries + 1
        /\ deliveredTo' = dest[m]
        /\ pc' = [pc EXCEPT ![m] = "idle"]
        /\ view' = [view EXCEPT ![m] = "none"]
     \/ /\ pc' = [pc EXCEPT ![m] = "undelivered"]
        /\ UNCHANGED <<deliveries, deliveredTo, view>>
  /\ UNCHANGED <<memo, to, dest, attempts, sessions, live, seen, task, taskMemo, adoptedLive>>

(* 取ったメモを、新しいセッションを起こして渡す。入力の検査で拒まれれば何も起きず戻しに進む。window を作れば claude が
   前景になるのを待ち、なれば届いたことになる。待ち切れなければ pane は残り claude が起動していることもある。起きたか
   分からないメモは戻さず handed のまま残し、人に pane を知らせる (spawn.go の waitForClaude が foreground=false で返し、
   parking.ts の handOff は戻さない)。戻すと押し直しで 2 つ目が起きる。宛先は起こす先の repo のまま書き直さない。 *)
Spawn(m) ==
  /\ pc[m] = "claimed" /\ dest[m] = "repo"
  /\ \/ /\ pc' = [pc EXCEPT ![m] = "undelivered"]                   \* check で拒否
        /\ UNCHANGED <<deliveries, deliveredTo, sessions, view>>
     \/ /\ sessions' = sessions + 1 /\ deliveries' = deliveries + 1 \* 前景になった
        /\ deliveredTo' = dest[m]
        /\ pc' = [pc EXCEPT ![m] = "idle"]
        /\ view' = [view EXCEPT ![m] = "none"]
     \/ /\ sessions' = sessions + 1                                 \* 待ち切れ。claude は起きているかもしれない
        /\ pc' = [pc EXCEPT ![m] = "idle"]
        /\ view' = [view EXCEPT ![m] = "none"]
        /\ UNCHANGED <<deliveries, deliveredTo>>
  /\ UNCHANGED <<memo, to, dest, attempts, live, seen, task, taskMemo, adoptedLive>>

(* 届かなかったメモを park set (status open、from handed) で戻し、宛先を空にする。プロセスが失敗すれば handed のまま
   残る (人が park set で戻す)。 *)
Revert(m) ==
  /\ pc[m] = "undelivered"
  /\ pc' = [pc EXCEPT ![m] = "idle"]
  /\ view' = [view EXCEPT ![m] = "none"]
  /\ \/ memo' = "open" /\ to' = None
     \/ UNCHANGED <<memo, to>>
  /\ UNCHANGED <<dest, deliveredTo, attempts, deliveries, sessions, live, seen, task, taskMemo, adoptedLive>>

(* pane の Discard。読んだ依存図で open だったメモを park set (status discarded、from open) で捨てる。
   先に取られていれば失敗する。置いたセッションの unpark (park withdraw) も open のときだけ discarded にするので、
   同じ遷移として扱う。 *)
Discard(m) ==
  /\ pc[m] = "idle" /\ view[m] = "open"
  /\ view' = [view EXCEPT ![m] = "none"]
  /\ \/ /\ memo = "open" /\ memo' = "discarded"
     \/ UNCHANGED memo
  /\ UNCHANGED <<handoff, pc, attempts, deliveries, sessions, live, seen, task, taskMemo, adoptedLive>>

-----------------------------------------------------------------------------
(* タスクの記録を持つセッションが終了する。記録は残る (daemon の housekeeping が消すまで)。 *)
SessionExit == /\ live /\ live' = FALSE
               /\ UNCHANGED <<memo, handoff, view, pc, attempts, deliveries, sessions, seen, task, taskMemo, adoptedLive>>

(* daemon の tick。今の pane の一覧を status.json に書く (daemon/status.go)。 *)
DaemonTick == /\ seen' = IF live THEN "live" ELSE "exited"
              /\ UNCHANGED <<memo, handoff, view, pc, attempts, deliveries, sessions, live, task, taskMemo, adoptedLive>>

(* daemon が止まる・入れ替わる。止まっている間と、前の daemon が残した status.json しか無い間は、deps は pane を
   空として扱い (deps.go の livePanes)、Adopt は受け付けない。 *)
DaemonStop == /\ seen /= "none" /\ seen' = "none"
              /\ UNCHANGED <<memo, handoff, view, pc, attempts, deliveries, sessions, live, task, taskMemo, adoptedLive>>

(* pane の To parking。Adopt は、今の daemon がタスクの記録より後に書いた status.json にそのセッションの pane が
   無いときだけメモを書く (daemonstate の SessionExited)。daemon が止まっているときに exited と描かれても通さない。 *)
AdoptWrite(m) ==
  /\ pc[m] = "idle" /\ task /\ seen = "exited"
  /\ taskMemo' = TRUE
  /\ pc' = [pc EXCEPT ![m] = "adopting"]
  /\ UNCHANGED <<memo, handoff, view, attempts, deliveries, sessions, live, seen, task, adoptedLive>>

(* Adopt の 2 手目。タスクの記録を消す。消し損ねればメモだけが残る (place.go の Adopt)。 *)
AdoptRemove(m) ==
  /\ pc[m] = "adopting"
  /\ pc' = [pc EXCEPT ![m] = "idle"]
  /\ \/ /\ task' = FALSE /\ adoptedLive' = (adoptedLive \/ live)
     \/ UNCHANGED <<task, adoptedLive>>
  /\ UNCHANGED <<memo, handoff, view, attempts, deliveries, sessions, live, seen, taskMemo>>

-----------------------------------------------------------------------------
Next ==
  \/ \E m \in Mods :
       \/ \E d \in Dests : Claim(m, d)
       \/ Record(m) \/ Read(m) \/ Send(m) \/ Spawn(m) \/ Revert(m) \/ Discard(m) \/ AdoptWrite(m) \/ AdoptRemove(m)
  \/ SessionExit \/ DaemonTick \/ DaemonStop

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

\* 6. handed のメモは宛先を持つ
HandedHasDestination == memo = "handed" => to # None

\* 7. handed でないメモは宛先を持たない (戻したら空にする)
OnlyHandedHasDestination == memo # "handed" => to = None

\* 8. 届いたメモの宛先は、届いた先と同じ
DeliveredWhereRecorded == deliveredTo # None => to = deliveredTo

=============================================================================
