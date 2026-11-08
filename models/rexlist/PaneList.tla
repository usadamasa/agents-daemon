---------------------------- MODULE PaneList ----------------------------
(* rex の全列挙 (rexcli の snapshot) と、その途中で session が消える交錯を写したモデル。
   全列挙は daemon の購読が未同期の間の PaneList と、購読を張り直すときの watchOnce が使う。

   - 全列挙は `ls --json` で session の一覧を 1 回で取り、その一覧の session に 1 つずつ `block ls -s S` を打つ。
     session の順は決めず、残りのどれからでも打てることにして広く探す。
   - 利用者はいつでも session を閉じられる (Close)。閉じた session への `block ls` は exit 3 (target not found) になる。
   - rex の server はいつでも落ちうる (Down)。落ちた後の `block ls` は失敗し、これは本物の障害。
   - SkipClosed が TRUE なら、`block ls` の exit 3 を「ls の後に閉じた session」として読み飛ばす
     (block call の exit 3 を読み飛ばすのと同じ扱い)。FALSE なら全列挙の失敗にする。
   - 全列挙の失敗は daemon の tick の連続失敗に数え、10 回で daemon が終わる。購読の張り直しでは backoff になる。
     ここでは 1 回の全列挙の結果 (ok / fail) だけを見る。 *)

CONSTANTS
  Sessions,    \* 全列挙を始めたときにある session
  SkipClosed   \* `block ls` の exit 3 を読み飛ばすか

VARIABLES
  live,     \* 今ある session
  down,     \* rex の server が落ちたか
  pc,       \* 全列挙の段: idle / listing / done
  listed,   \* `ls` が返した session
  todo,     \* `block ls` をまだ打っていない session
  skipped,  \* exit 3 で読み飛ばした session
  result    \* 全列挙の結果: none / ok / fail

vars == <<live, down, pc, listed, todo, skipped, result>>

TypeOK ==
  /\ live \subseteq Sessions
  /\ down \in BOOLEAN
  /\ pc \in {"idle", "listing", "done"}
  /\ listed \subseteq Sessions
  /\ todo \subseteq listed
  /\ skipped \subseteq listed
  /\ result \in {"none", "ok", "fail"}

Init ==
  /\ live = Sessions
  /\ down = FALSE
  /\ pc = "idle"
  /\ listed = {}
  /\ todo = {}
  /\ skipped = {}
  /\ result = "none"

\* 利用者が session を閉じる (rex は session_destroyed を流し、以降その session は解決できない)。
Close(s) ==
  /\ s \in live
  /\ live' = live \ {s}
  /\ UNCHANGED <<down, pc, listed, todo, skipped, result>>

\* rex の server が落ちる (更新の再起動、クラッシュ)。
Down ==
  /\ ~down
  /\ down' = TRUE
  /\ UNCHANGED <<live, pc, listed, todo, skipped, result>>

\* `ls --json`: 今ある session を 1 回で取る。server が落ちていれば失敗する。
List ==
  /\ pc = "idle"
  /\ IF down
       THEN /\ result' = "fail"
            /\ pc' = "done"
            /\ UNCHANGED <<listed, todo>>
       ELSE /\ listed' = live
            /\ todo' = live
            /\ pc' = "listing"
            /\ UNCHANGED result
  /\ UNCHANGED <<live, down, skipped>>

\* `block ls -s S`: server が落ちていれば失敗。閉じた session は exit 3 で、SkipClosed なら読み飛ばす。
BlockLs(s) ==
  /\ pc = "listing"
  /\ s \in todo
  /\ todo' = todo \ {s}
  /\ CASE down ->
            /\ result' = "fail"
            /\ pc' = "done"
            /\ UNCHANGED skipped
       [] s \notin live /\ SkipClosed ->
            /\ skipped' = skipped \cup {s}
            /\ UNCHANGED <<result, pc>>
       [] s \notin live ->
            /\ result' = "fail"
            /\ pc' = "done"
            /\ UNCHANGED skipped
       [] OTHER ->
            UNCHANGED <<result, pc, skipped>>
  /\ UNCHANGED <<live, down, listed>>

\* 全部の session に打ち終えた。
Finish ==
  /\ pc = "listing"
  /\ todo = {}
  /\ result' = "ok"
  /\ pc' = "done"
  /\ UNCHANGED <<live, down, listed, todo, skipped>>

Idle == pc = "done" /\ UNCHANGED vars

Next ==
  \/ \E s \in Sessions : Close(s) \/ BlockLs(s)
  \/ Down \/ List \/ Finish \/ Idle

Spec == Init /\ [][Next]_vars

\* 全列挙が失敗するのは server が落ちたときだけ。session を閉じただけで失敗に数えない。
FailOnlyWhenDown == result = "fail" => down

\* 読み飛ばすのは閉じた session だけ。ある session の block を取りこぼさない。
SkipOnlyClosed == skipped \cap live = {}

\* 成功した全列挙は、`ls` が返した session を全部打ち終えている。
OkCoversListed == result = "ok" => todo = {}

=============================================================================
