---------------------------- MODULE Card ----------------------------
(* 依存図のカードの状態を、記録の更新と保持期間の掃除から導くモデル (#483)。

   セッション 1 つとその記録 (tasks/、work/)、そのセッションを before に持つ辺 1 本、open の PR を before に持つ辺 1 本を追う。
   カードの状態は実装と同じ式で導く (SessionCard は deps の collect、NodeState は graph の nodeState、UpstreamState は graph の
   sinkHub と sessionKeys)。経過は保持期間の区切りだけを写す (0 は 1 日未満、1 は 7 日未満、2 は 7 日以上)。
   housekeeping は作業の記録、辺、タスクの順に 1 回で消す。作業の記録が消える回の順は PruneWork が 1 ステップで写す。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  MaxTasks,      \* セッションが作るタスクの数
  MaxResumes,    \* resume の回数の上限
  KeepCompleted, \* 終了したセッションの完了タスクを、作業の記録が消えるまで掃除しないか
  TouchLive      \* 生きているセッションの辺と未完了タスク、open の PR の辺の mtime を tick ごとに進めるか

Tasks == 1..MaxTasks
Progress == {"none", "pending", "in_progress", "completed"}
Activities == {"working", "done", "permission"}
Open == {"pending", "in_progress"}

VARIABLES
  alive,         \* セッションの pane があるか
  act,           \* セッションの活動
  tasks,         \* tasks/<sid>/ の記録 (none は記録が無い)
  work,          \* work/<sid>.json があるか
  taskAge,       \* タスクの記録が最後に書かれてからの経過 (完了タスクの掃除に使う)
  openAge,       \* 未完了のタスクの mtime からの経過
  workAge,       \* 作業の記録の mtime からの経過 (pane が見えている間は Touch で 0)
  edgeAge,       \* 辺の mtime からの経過
  edge,          \* sid: → 下流のタスク の辺があるか
  prOpen,        \* before の PR が open か
  prEdge,        \* pr → 下流のタスク の辺があるか
  prAge,         \* PR の辺の mtime からの経過
  resumes,       \* resume した回数
  exitedAllDone, \* 終了した時点でタスクの記録を持ち、全部 completed だった (resume で落とす)
  lost           \* 保持期間の掃除が消してしまった、まだ要る記録の種類

vars == <<alive, act, tasks, work, taskAge, openAge, workAge, edgeAge, edge, prOpen, prEdge, prAge, resumes, exitedAllDone, lost>>

TypeOK ==
  /\ alive \in BOOLEAN
  /\ act \in Activities
  /\ tasks \in [Tasks -> Progress]
  /\ work \in BOOLEAN
  /\ taskAge \in 0..2 /\ openAge \in 0..2 /\ workAge \in 0..2 /\ edgeAge \in 0..2 /\ prAge \in 0..2
  /\ edge \in BOOLEAN /\ prOpen \in BOOLEAN /\ prEdge \in BOOLEAN
  /\ resumes \in 0..MaxResumes
  /\ exitedAllDone \in BOOLEAN
  /\ lost \subseteq {"task", "edge", "pr"}

--------------------------------------------------------------------------------
\* 実装と同じ式で導く値

HasTask == \E t \in Tasks : tasks[t] /= "none"
InProgress == \E t \in Tasks : tasks[t] = "in_progress"
TaskKeys == {t \in Tasks : tasks[t] /= "none"}
AllDone == HasTask /\ \A t \in TaskKeys : tasks[t] = "completed"

\* collect。"work" は作業の記録から、"pane" は pane から作るセッションのカード、"none" は作らない。
SessionCard ==
  IF work /\ ~(HasTask /\ (InProgress \/ ~alive)) THEN "work"
  ELSE IF alive /\ ~HasTask /\ ~work THEN "pane"
  ELSE "none"

StatusOnly == SessionCard = "work" /\ HasTask

\* nodeState
NodeState(p) ==
  IF p = "completed" THEN "completed"
  ELSE IF ~alive THEN "exited"
  ELSE IF p /= "in_progress" THEN "pending"
  ELSE act

\* sinkHub と sessionKeys。blockedBy は置かないので、記録のあるタスクは全部が末端。
UpstreamState ==
  IF SessionCard /= "none" /\ ~StatusOnly THEN NodeState("in_progress")
  ELSE IF TaskKeys = {} THEN "exited"
  ELSE IF Cardinality(TaskKeys) = 1 THEN NodeState(tasks[CHOOSE t \in TaskKeys : TRUE])
  ELSE IF AllDone THEN "completed"
  ELSE NodeState("in_progress")

\* 完了を隠した図に出る、このセッションのカードの状態の集合。
ShownStates ==
  ({NodeState(tasks[t]) : t \in TaskKeys} \ {"completed"})
    \cup (IF SessionCard /= "none" THEN {NodeState("in_progress")} ELSE {})

--------------------------------------------------------------------------------
\* セッションの出来事

Records == <<taskAge, openAge, edgeAge, edge, prOpen, prEdge, prAge, resumes, exitedAllDone, lost>>

Prompt ==
  /\ alive /\ act' = "working" /\ work' = TRUE /\ workAge' = 0
  /\ UNCHANGED <<alive, tasks, Records>>

Stop ==
  /\ alive /\ act = "working" /\ act' = "done" /\ work' = TRUE /\ workAge' = 0
  /\ UNCHANGED <<alive, tasks, Records>>

Block ==
  /\ alive /\ act = "working" /\ act' = "permission"
  /\ UNCHANGED <<alive, tasks, work, workAge, Records>>

Approve ==
  /\ alive /\ act = "permission" /\ act' = "working"
  /\ UNCHANGED <<alive, tasks, work, workAge, Records>>

\* タスクの記録が掃除で消えた後の TaskCreate は写さない (id は増える一方で、同じ t は作り直さない前提を none からの遷移で近似する)。
SetTask(t, from, to) ==
  /\ alive /\ act = "working" /\ tasks[t] = from
  /\ tasks' = [tasks EXCEPT ![t] = to]
  /\ taskAge' = 0 /\ openAge' = 0
  /\ UNCHANGED <<alive, act, work, workAge, edgeAge, edge, prOpen, prEdge, prAge, resumes, exitedAllDone, lost>>

TaskStep(t) ==
  \/ SetTask(t, "none", "pending")
  \/ SetTask(t, "pending", "in_progress")
  \/ SetTask(t, "in_progress", "completed")

Exit ==
  /\ alive
  /\ alive' = FALSE
  /\ exitedAllDone' = AllDone
  /\ workAge' = 0  \* SessionEnd の hook が記録を書く
  /\ UNCHANGED <<act, tasks, work, taskAge, openAge, edgeAge, edge, prOpen, prEdge, prAge, resumes, lost>>

\* 作業の記録は resume の SessionStart の hook が書く。未完了のタスクと辺は次の tick の Touch で進む。掃除は 1 時間ごとなので tick を先に置く。
Resume ==
  /\ ~alive /\ resumes < MaxResumes
  /\ alive' = TRUE /\ act' = "done"
  /\ resumes' = resumes + 1
  /\ exitedAllDone' = FALSE
  /\ workAge' = 0
  /\ openAge' = IF TouchLive THEN 0 ELSE openAge
  /\ edgeAge' = IF TouchLive THEN 0 ELSE edgeAge
  /\ UNCHANGED <<tasks, work, taskAge, edge, prOpen, prEdge, prAge, lost>>

Merge ==
  /\ prOpen /\ prOpen' = FALSE
  /\ UNCHANGED <<alive, act, tasks, work, taskAge, openAge, workAge, edgeAge, edge, prEdge, prAge, resumes, exitedAllDone, lost>>

--------------------------------------------------------------------------------
\* housekeeping と daemon の tick

Min2(x) == IF x >= 2 THEN 2 ELSE x + 1

\* 経過が進む。tick の Touch が要るものは、生きている間 (PR は open の間) 0 のまま。
Advance ==
  LET t == Min2(taskAge)
      o == IF alive /\ TouchLive THEN 0 ELSE Min2(openAge)
      w == IF alive THEN 0 ELSE Min2(workAge)
      e == IF alive /\ TouchLive THEN 0 ELSE Min2(edgeAge)
      p == IF prOpen /\ TouchLive THEN 0 ELSE Min2(prAge)
  IN /\ <<t, o, w, e, p>> /= <<taskAge, openAge, workAge, edgeAge, prAge>>
     /\ taskAge' = t /\ openAge' = o /\ workAge' = w /\ edgeAge' = e /\ prAge' = p
     /\ UNCHANGED <<alive, act, tasks, work, edge, prOpen, prEdge, resumes, exitedAllDone, lost>>

\* 掃除しないのは、終了したセッションの完了タスクで作業の記録が残っているもの。
KeptCompleted == KeepCompleted /\ work /\ ~alive

PrunableTask(t) ==
  \/ (tasks[t] = "completed" /\ taskAge >= 1 /\ ~KeptCompleted)
  \/ (tasks[t] \in Open /\ openAge = 2)

PruneTask(t) ==
  /\ PrunableTask(t)
  /\ tasks' = [tasks EXCEPT ![t] = "none"]
  /\ lost' = IF alive /\ tasks[t] \in Open THEN lost \cup {"task"} ELSE lost
  /\ UNCHANGED <<alive, act, work, taskAge, openAge, workAge, edgeAge, edge, prOpen, prEdge, prAge, resumes, exitedAllDone>>

\* 作業の記録が消える回に、期限の来た辺と、残していた完了タスクも消える。
PruneWork ==
  /\ work /\ workAge = 2
  /\ work' = FALSE
  /\ edge' = (edge /\ edgeAge < 2)
  /\ tasks' = [t \in Tasks |->
                 IF (tasks[t] = "completed" /\ taskAge >= 1) \/ (tasks[t] \in Open /\ openAge = 2)
                 THEN "none" ELSE tasks[t]]
  /\ UNCHANGED <<alive, act, taskAge, openAge, workAge, edgeAge, prOpen, prEdge, prAge, resumes, exitedAllDone, lost>>

PruneEdge ==
  /\ edge /\ edgeAge = 2
  /\ edge' = FALSE
  /\ lost' = IF alive THEN lost \cup {"edge"} ELSE lost
  /\ UNCHANGED <<alive, act, tasks, work, taskAge, openAge, workAge, edgeAge, prOpen, prEdge, prAge, resumes, exitedAllDone>>

PrunePrEdge ==
  /\ prEdge /\ prAge = 2
  /\ prEdge' = FALSE
  /\ lost' = IF prOpen THEN lost \cup {"pr"} ELSE lost
  /\ UNCHANGED <<alive, act, tasks, work, taskAge, openAge, workAge, edgeAge, edge, prOpen, prAge, resumes, exitedAllDone>>

\* 済んだ PR の辺を消す (refpoll の sweep)。
Sweep ==
  /\ prEdge /\ ~prOpen /\ prEdge' = FALSE
  /\ UNCHANGED <<alive, act, tasks, work, taskAge, openAge, workAge, edgeAge, edge, prOpen, prAge, resumes, exitedAllDone, lost>>

Housekeep ==
  \/ Advance
  \/ \E t \in Tasks : PruneTask(t)
  \/ PruneWork \/ PruneEdge \/ PrunePrEdge \/ Sweep

Next ==
  \/ Prompt \/ Stop \/ Block \/ Approve
  \/ \E t \in Tasks : TaskStep(t)
  \/ Exit \/ Resume \/ Merge
  \/ Housekeep

Init ==
  /\ alive = TRUE /\ act = "done"
  /\ tasks = [t \in Tasks |-> "none"]
  /\ work = FALSE
  /\ taskAge = 0 /\ openAge = 0 /\ workAge = 0 /\ edgeAge = 0 /\ prAge = 0
  /\ edge = TRUE /\ prOpen = TRUE /\ prEdge = TRUE
  /\ resumes = 0 /\ exitedAllDone = FALSE
  /\ lost = {}

Spec == Init /\ [][Next]_vars /\ WF_vars(Housekeep)

--------------------------------------------------------------------------------
\* 性質

\* 生きているセッションには活動の置き場がある (#489)。
LivePlace == alive => (InProgress \/ SessionCard /= "none")

\* 生きているセッションのカードを終了と見せない。
NoFalseExited == alive => "exited" \notin ShownStates

\* タスクを全部終えて終了したセッションを、掃除の後に終了のカードとして見せない。
NoResurrectedExited == (exitedAllDone /\ ~alive) => "exited" \notin ShownStates

\* タスクを全部終えて終了したセッションを待つ辺は、済んだまま変わらない。
SatisfiedStays == (exitedAllDone /\ ~alive /\ edge) => UpstreamState = "completed"

\* 生きているセッションの未完了のタスクを保持期間で消さない。
LiveTaskKept == "task" \notin lost

\* 生きているセッションの辺を保持期間で消さない。
LiveEdgeKept == "edge" \notin lost

\* open の PR を before に持つ辺を保持期間で消さない。
OpenRefEdgeKept == "pr" \notin lost

\* 終了したセッションのカードはいずれ図から抜ける。
EventuallyLeaves == <>[](~alive) => <>[](ShownStates = {})

=============================================================================
