---------------------------- MODULE Card ----------------------------
(* 依存図のカードの状態を、記録の更新と保持期間の掃除から導くモデル (#483)。

   セッション 1 つとその記録 (tasks/、work/)、そのセッションを before に持つ辺 1 本を追う。カードの状態は実装と同じ式で導く
   (SessionCard は deps の collect、NodeState は graph の nodeState、UpstreamState は graph の sinkHub と sessionKeys)。
   経過は保持期間の区切りだけを写す (0 は 1 日未満、1 は 7 日未満、2 は 7 日以上)。
   housekeeping はタスク、作業の記録、辺の順に消す。PruneWork と PruneTask の前提でこの順を写す。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  MaxTasks,      \* セッションが作るタスクの数
  MaxResumes,    \* resume の回数の上限
  KeepCompleted  \* 終了したセッションの完了タスクを、作業の記録が消えるまで掃除しないか

Tasks == 1..MaxTasks
Progress == {"none", "pending", "in_progress", "completed"}
Activities == {"working", "done", "permission"}

VARIABLES
  alive,         \* セッションの pane があるか
  act,           \* セッションの活動
  tasks,         \* tasks/<sid>/ の記録 (none は記録が無い)
  work,          \* work/<sid>.json があるか
  taskAge,       \* タスクの記録が最後に書かれてからの経過
  workAge,       \* 作業の記録の mtime からの経過 (pane が見えている間は Touch で 0)
  edgeAge,       \* 辺が最後に書かれてからの経過
  edge,          \* sid: → 下流のタスク の辺があるか
  resumes,       \* resume した回数
  exitedAllDone  \* 終了した時点でタスクの記録を持ち、全部 completed だった (resume で落とす)

vars == <<alive, act, tasks, work, taskAge, workAge, edgeAge, edge, resumes, exitedAllDone>>

TypeOK ==
  /\ alive \in BOOLEAN
  /\ act \in Activities
  /\ tasks \in [Tasks -> Progress]
  /\ work \in BOOLEAN
  /\ taskAge \in 0..2 /\ workAge \in 0..2 /\ edgeAge \in 0..2
  /\ edge \in BOOLEAN
  /\ resumes \in 0..MaxResumes
  /\ exitedAllDone \in BOOLEAN

--------------------------------------------------------------------------------
\* 実装と同じ式で導く値

HasTask == \E t \in Tasks : tasks[t] /= "none"
InProgress == \E t \in Tasks : tasks[t] = "in_progress"
TaskKeys == {t \in Tasks : tasks[t] /= "none"}
AllDone == HasTask /\ \A t \in TaskKeys : tasks[t] = "completed"

\* collect。"work" は作業の記録から、"pane" は pane から作るセッションのカード、"none" は作らない。
SessionCard ==
  IF work /\ ~(HasTask /\ (InProgress \/ ~alive)) THEN "work"
  ELSE IF alive /\ ~InProgress /\ ~work THEN "pane"
  ELSE "none"

StatusOnly == SessionCard /= "none" /\ HasTask

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

Prompt ==
  /\ alive /\ act' = "working" /\ work' = TRUE /\ workAge' = 0
  /\ UNCHANGED <<alive, tasks, taskAge, edgeAge, edge, resumes, exitedAllDone>>

Stop ==
  /\ alive /\ act = "working" /\ act' = "done" /\ work' = TRUE /\ workAge' = 0
  /\ UNCHANGED <<alive, tasks, taskAge, edgeAge, edge, resumes, exitedAllDone>>

Block ==
  /\ alive /\ act = "working" /\ act' = "permission"
  /\ UNCHANGED <<alive, tasks, work, taskAge, workAge, edgeAge, edge, resumes, exitedAllDone>>

Approve ==
  /\ alive /\ act = "permission" /\ act' = "working"
  /\ UNCHANGED <<alive, tasks, work, taskAge, workAge, edgeAge, edge, resumes, exitedAllDone>>

\* タスクの記録が掃除で消えた後の TaskCreate は写さない (id は増える一方で、同じ t は作り直さない前提を none からの遷移で近似する)。
SetTask(t, from, to) ==
  /\ alive /\ act = "working" /\ tasks[t] = from
  /\ tasks' = [tasks EXCEPT ![t] = to]
  /\ taskAge' = 0
  /\ UNCHANGED <<alive, act, work, workAge, edgeAge, edge, resumes, exitedAllDone>>

TaskStep(t) ==
  \/ SetTask(t, "none", "pending")
  \/ SetTask(t, "pending", "in_progress")
  \/ SetTask(t, "in_progress", "completed")

Exit ==
  /\ alive
  /\ alive' = FALSE
  /\ exitedAllDone' = AllDone
  /\ workAge' = 0  \* SessionEnd の hook が記録を書く
  /\ UNCHANGED <<act, tasks, work, taskAge, edgeAge, edge, resumes>>

Resume ==
  /\ ~alive /\ resumes < MaxResumes
  /\ alive' = TRUE /\ act' = "done"
  /\ resumes' = resumes + 1
  /\ exitedAllDone' = FALSE
  /\ workAge' = 0  \* resume の SessionStart の hook が記録を書く
  /\ UNCHANGED <<tasks, work, taskAge, edgeAge, edge>>

--------------------------------------------------------------------------------
\* housekeeping

Min2(x) == IF x >= 2 THEN 2 ELSE x + 1

Advance ==
  /\ taskAge < 2 \/ edgeAge < 2 \/ (workAge < 2 /\ ~alive)
  /\ taskAge' = Min2(taskAge)
  /\ edgeAge' = Min2(edgeAge)
  /\ workAge' = IF alive THEN 0 ELSE Min2(workAge)
  /\ UNCHANGED <<alive, act, tasks, work, edge, resumes, exitedAllDone>>

\* 掃除しないのは、終了したセッションの完了タスクで作業の記録が残っているもの。
KeptCompleted == KeepCompleted /\ work /\ ~alive

PrunableTask(t) ==
  \/ (tasks[t] = "completed" /\ taskAge >= 1 /\ ~KeptCompleted)
  \/ (tasks[t] \in {"pending", "in_progress"} /\ taskAge = 2)

\* 期限の来た辺は、同じ回の housekeeping で作業の記録の後に消える。次の回に走るタスクの掃除より先。
PruneTask(t) ==
  /\ PrunableTask(t)
  /\ KeepCompleted /\ ~work /\ ~alive => ~(edge /\ edgeAge = 2)
  /\ tasks' = [tasks EXCEPT ![t] = "none"]
  /\ UNCHANGED <<alive, act, work, taskAge, workAge, edgeAge, edge, resumes, exitedAllDone>>

PruneWork ==
  /\ work /\ workAge = 2
  /\ \A t \in Tasks : ~PrunableTask(t)
  /\ work' = FALSE
  /\ UNCHANGED <<alive, act, tasks, taskAge, workAge, edgeAge, edge, resumes, exitedAllDone>>

PruneEdge ==
  /\ edge /\ edgeAge = 2
  /\ edge' = FALSE
  /\ UNCHANGED <<alive, act, tasks, work, taskAge, workAge, edgeAge, resumes, exitedAllDone>>

Housekeep ==
  \/ Advance
  \/ \E t \in Tasks : PruneTask(t)
  \/ PruneWork \/ PruneEdge

Next ==
  \/ Prompt \/ Stop \/ Block \/ Approve
  \/ \E t \in Tasks : TaskStep(t)
  \/ Exit \/ Resume
  \/ Housekeep

Init ==
  /\ alive = TRUE /\ act = "done"
  /\ tasks = [t \in Tasks |-> "none"]
  /\ work = FALSE
  /\ taskAge = 0 /\ workAge = 0 /\ edgeAge = 0
  /\ edge = TRUE
  /\ resumes = 0 /\ exitedAllDone = FALSE

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

\* 終了したセッションのカードはいずれ図から抜ける。
EventuallyLeaves == <>[](~alive) => <>[](ShownStates = {})

=============================================================================
