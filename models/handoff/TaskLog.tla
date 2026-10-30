---------------------------- MODULE TaskLog ----------------------------
(* タスクの記録 (tasklog) を PostToolUse hook が書く手順を写したモデル。

   - TaskCreate の hook はそのタスクの記録を書く (tasklog.go の create)。
   - TaskList の hook は tool_response の一覧で全件を同期し直し、一覧に無い記録のうち、一覧の最大の id より小さい id
     のものだけを消す (tasklog.go の sync)。Claude Code のタスクの id はセッションの中で 1 から順に増える整数なので、
     一覧の最大の id より大きい記録は、一覧を取った後に TaskCreate が書いたもの。
   - 同じターンで並列に呼ばれた tool の hook は順序が決まらない (tasklog.go の package doc はタスクが別なら取り合わないとする)。
   一覧は tool が実行された時点のスナップショットで、hook はその後に動く。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS MaxTasks  \* Claude Code がこのセッションで作るタスクの数の上限 (id は 1..MaxTasks)

Tasks == 1..MaxTasks

VARIABLES
  ccTasks,  \* Claude Code 本体が持つタスクの集合
  records,  \* tasks/<session>/ に記録があるタスクの集合
  hooks     \* まだ動いていない hook の入力の集合

vars == <<ccTasks, records, hooks>>

HookInput == [kind : {"create"}, task : Tasks] \cup [kind : {"list"}, tasks : SUBSET Tasks]

TypeOK ==
  /\ ccTasks \subseteq Tasks
  /\ records \subseteq Tasks
  /\ hooks \subseteq HookInput

Init == ccTasks = {} /\ records = {} /\ hooks = {}

\* 一覧の最大の id。空なら 0 (どの記録も「後から作られた」になる)。
Max(S) == IF S = {} THEN 0 ELSE CHOOSE x \in S : \A y \in S : y <= x

\* model が TaskCreate を呼ぶ。id は次の整数。hook の入力はそのタスク。
Create ==
  /\ Cardinality(ccTasks) < MaxTasks
  /\ LET t == Cardinality(ccTasks) + 1 IN
       /\ ccTasks' = ccTasks \cup {t}
       /\ hooks' = hooks \cup {[kind |-> "create", task |-> t]}
  /\ UNCHANGED records

\* model が TaskList を呼ぶ。hook の入力はその時点の一覧。
List ==
  /\ hooks' = hooks \cup {[kind |-> "list", tasks |-> ccTasks]}
  /\ UNCHANGED <<ccTasks, records>>

\* hook が動く。順序は決まらない。list は一覧の最大の id より大きい記録を残す。
RunHook(h) ==
  /\ h \in hooks
  /\ hooks' = hooks \ {h}
  /\ records' = IF h.kind = "create" THEN records \cup {h.task}
                ELSE h.tasks \cup {r \in records : r > Max(h.tasks)}
  /\ UNCHANGED ccTasks

Next == Create \/ List \/ (\E h \in hooks : RunHook(h))

Spec == Init /\ [][Next]_vars

\* TaskCreate が書いた記録は失われない: hook が全部動き終わった時点で、本体のタスクは全て記録にある
RecordsComplete == hooks = {} => ccTasks \subseteq records

=============================================================================
