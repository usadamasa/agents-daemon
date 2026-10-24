---------------------------- MODULE TaskLog ----------------------------
(* タスクの記録 (internal/tasklog) を PostToolUse hook が書く手順を写したモデル。

   - TaskCreate の hook はそのタスクの記録を書く (tasklog.go:238)。
   - TaskList の hook は tool_response の一覧で全件を同期し直し、一覧に無い記録は消す (tasklog.go:286-333)。
   - 同じターンで並列に呼ばれた tool の hook は順序が決まらない (tasklog.go:7-9 はタスクが別なら取り合わないとする)。
   一覧は tool が実行された時点のスナップショットで、hook はその後に動く。 *)
EXTENDS FiniteSets

CONSTANTS Tasks  \* Claude Code が作りうるタスクの集合

VARIABLES
  ccTasks,  \* Claude Code 本体が持つタスクの集合
  records,  \* tasks/<domain>/<session>/ に記録があるタスクの集合
  hooks     \* まだ動いていない hook の入力の集合

vars == <<ccTasks, records, hooks>>

HookInput == [kind : {"create"}, task : Tasks] \cup [kind : {"list"}, tasks : SUBSET Tasks]

TypeOK ==
  /\ ccTasks \subseteq Tasks
  /\ records \subseteq Tasks
  /\ hooks \subseteq HookInput

Init == ccTasks = {} /\ records = {} /\ hooks = {}

\* model が TaskCreate を呼ぶ。hook の入力はそのタスク。
Create(t) ==
  /\ t \notin ccTasks
  /\ ccTasks' = ccTasks \cup {t}
  /\ hooks' = hooks \cup {[kind |-> "create", task |-> t]}
  /\ UNCHANGED records

\* model が TaskList を呼ぶ。hook の入力はその時点の一覧。
List ==
  /\ hooks' = hooks \cup {[kind |-> "list", tasks |-> ccTasks]}
  /\ UNCHANGED <<ccTasks, records>>

\* hook が動く。順序は決まらない。
RunHook(h) ==
  /\ h \in hooks
  /\ hooks' = hooks \ {h}
  /\ records' = IF h.kind = "create" THEN records \cup {h.task} ELSE h.tasks
  /\ UNCHANGED ccTasks

Next == (\E t \in Tasks : Create(t)) \/ List \/ (\E h \in hooks : RunHook(h))

Spec == Init /\ [][Next]_vars

\* TaskCreate が書いた記録は失われない: hook が全部動き終わった時点で、本体のタスクは全て記録にある
RecordsComplete == hooks = {} => ccTasks \subseteq records

=============================================================================
