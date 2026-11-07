---------------------------- MODULE Work ----------------------------
(* セッションの作業の記録 (work/<session_id>.json、worklog) を hook と companion と人が書く手順を写したモデル (#368)。

   - SessionStart の hook は記録があって resume なら ended_at を消す。無ければ作らない (worklog の Ingest)。
   - UserPromptSubmit の hook は記録が無ければ作り、subject が空なら prompt を subject にし (origin prompt)、turns を 1 増やす。
   - Stop の hook は記録が無ければ作り、summary を書く (ここでは写さない)。ended_at には触れない。
   - SessionEnd の hook は記録があれば ended_at を書く。prompt を打たずに終わったセッションの記録は作らない。
   - companion (mod) は turns が InferAfter 以上で推定の題が無い記録に、推定の題 (inferred) を書く (`work set`)。推定は時間が
     かかり、その間にほかの hook や人の編集が入る。
   - 人は `work set` で subject を直す (origin human)。hook も companion も human の subject を書き換えない。
   - prune は終了したセッションの記録を消す (housekeeping)。
   - Locked が TRUE なら、hook と companion の書き込みは work/.lock の中で読んでから書くまでを 1 ステップにし、自分の欄だけを
     書き換える。FALSE なら読むのと書くのが別のステップになり、読んだ写しをそのまま書く。
   - ファイルの書き込みは statefile.WriteJSONAtomic なので 1 回の書き込みを 1 ステップにしている。
   - 同じセッションの hook は順に走るが、peer の delivery の UserPromptSubmit はターンの途中に入り Stop と前後しうる。
     ここでは動いていない hook はどの順でも動けることにして、実際より広く探している。
   - hook はセッションの process の中で同期に走り切る (SessionStart は会話に入る前、UserPromptSubmit と Stop はターンの進みを
     止めて、SessionEnd は process が終わる前の 1.5 秒の予算で) ので、終了するときに残っている hook は無く、resume は
     SessionEnd の hook が残っている間は起きない。この前提を外すと、遅れた prompt の hook が SessionEnd の後に記録を作る
     (ended にならない)、resume の hook が SessionEnd の後に ended を消す、という順が見つかる。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  MaxPrompts,  \* prompt を打つ回数の上限
  MaxResumes,  \* resume する回数の上限
  MaxInfers,   \* companion を起こす回数の上限 (失敗した推定のやり直しを含む)
  InferAfter,  \* companion が起きるのに要る記録の turns
  Locked       \* 書き込みを work/.lock の中で読んでから書く (自分の欄だけを書き換える) か

None == "none"

Subjects == {None, "prompt", "human"}
Kinds == {"start", "resume", "prompt", "stop", "end"}

VARIABLES
  alive,     \* セッションが動いているか
  prompts,   \* 記録を最後に消してから打った prompt の数 (記録の turns と見比べる)
  stops,     \* 同じく Stop の数 (prompt より多くならない)
  resumes,   \* resume した回数
  work,      \* 記録: [present, subject, inferred, ended, turns]
  hooks,     \* まだ書いていない hook: [id, kind, pc, snap]。pc は pending か read、snap は read で読んだ写し
  nextId,    \* 次の hook の id
  infers,    \* companion を起こした回数
  inferring, \* companion が推定中か
  isnap,     \* companion が起きたときに読んだ写し
  humanSet,  \* 人が subject を直した (記録が消えるまで)
  endedSet   \* SessionEnd の hook が ended を書いた (resume の hook か prune まで)

vars == <<alive, prompts, stops, resumes, work, hooks, nextId, infers, inferring, isnap, humanSet, endedSet>>

Record == [present : BOOLEAN, subject : Subjects, inferred : BOOLEAN, ended : BOOLEAN, turns : Nat]

Fresh == [present |-> TRUE, subject |-> None, inferred |-> FALSE, ended |-> FALSE, turns |-> 0]
Absent == [present |-> FALSE, subject |-> None, inferred |-> FALSE, ended |-> FALSE, turns |-> 0]

TypeOK ==
  /\ alive \in BOOLEAN
  /\ prompts \in 0..MaxPrompts
  /\ stops \in 0..MaxPrompts
  /\ resumes \in 0..MaxResumes
  /\ work \in Record
  /\ hooks \subseteq [id : Nat, kind : Kinds, pc : {"pending", "read"}, snap : Record]
  /\ nextId \in Nat
  /\ infers \in 0..MaxInfers
  /\ inferring \in BOOLEAN
  /\ isnap \in Record
  /\ humanSet \in BOOLEAN
  /\ endedSet \in BOOLEAN

\* 起動直後。SessionStart の hook が動く前。
Init ==
  /\ alive = TRUE
  /\ prompts = 0 /\ stops = 0 /\ resumes = 0
  /\ work = Absent
  /\ hooks = {[id |-> 0, kind |-> "start", pc |-> "pending", snap |-> Absent]}
  /\ nextId = 1
  /\ infers = 0 /\ inferring = FALSE /\ isnap = Absent
  /\ humanSet = FALSE /\ endedSet = FALSE

Enqueue(kind) ==
  /\ hooks' = hooks \cup {[id |-> nextId, kind |-> kind, pc |-> "pending", snap |-> Absent]}
  /\ nextId' = nextId + 1

\* hook の kind ごとの書き換え。w は読んだ記録 (Locked なら今の記録)。記録を作るのは prompt と stop だけで、
\* SessionStart と SessionEnd は記録があるときだけ書き換える (prompt を打たずに終わったセッションの記録を残さない)。
Apply(kind, w) ==
  LET base == IF w.present THEN w ELSE Fresh IN
  CASE kind = "start" -> w
    [] kind = "resume" -> IF w.present THEN [w EXCEPT !.ended = FALSE] ELSE w
    [] kind = "prompt" -> [base EXCEPT !.subject = IF @ = None THEN "prompt" ELSE @, !.turns = @ + 1]
    [] kind = "stop" -> base
    [] kind = "end" -> IF w.present THEN [w EXCEPT !.ended = TRUE] ELSE w

\* 書いた後の印。SessionEnd が ended を書けば endedSet、resume の hook が消せば落とす。記録が無くて書かなかったときは
\* 変えない。
Marks(kind, w) ==
  /\ endedSet' = CASE kind = "end" /\ w.present -> TRUE [] kind = "resume" /\ w.present -> FALSE [] OTHER -> endedSet

--------------------------------------------------------------------------------
\* Claude Code 側の出来事

Prompt ==
  /\ alive /\ prompts < MaxPrompts
  /\ prompts' = prompts + 1
  /\ Enqueue("prompt")
  /\ UNCHANGED <<alive, stops, resumes, work, infers, inferring, isnap, humanSet, endedSet>>

Stop ==
  /\ alive /\ stops < prompts
  /\ stops' = stops + 1
  /\ Enqueue("stop")
  /\ UNCHANGED <<alive, prompts, resumes, work, infers, inferring, isnap, humanSet, endedSet>>

\* hook はセッションの process の中で同期に走り切る (SessionStart は会話に入る前、UserPromptSubmit と Stop はターンの
\* 進みを止めて) ので、SessionEnd が走るときに残っている hook は無い。companion の推定だけは process の外の時間で進む。
Exit ==
  /\ alive /\ hooks = {}
  /\ alive' = FALSE
  /\ Enqueue("end")
  /\ UNCHANGED <<prompts, stops, resumes, work, infers, inferring, isnap, humanSet, endedSet>>

\* SessionEnd の hook が残っている間は resume しない (process が終わる前に走り切る)。
Resume ==
  /\ ~alive /\ resumes < MaxResumes
  /\ \A h \in hooks : h.kind /= "end"
  /\ alive' = TRUE
  /\ resumes' = resumes + 1
  /\ Enqueue("resume")
  /\ UNCHANGED <<prompts, stops, work, infers, inferring, isnap, humanSet, endedSet>>

--------------------------------------------------------------------------------
\* hook の書き込み

\* Locked: 読んでから書くまでを 1 ステップにし、自分の欄だけを書き換える。
RunHook(h) ==
  /\ Locked
  /\ hooks' = hooks \ {h}
  /\ work' = Apply(h.kind, work)
  /\ Marks(h.kind, work)
  /\ UNCHANGED <<alive, prompts, stops, resumes, nextId, infers, inferring, isnap, humanSet>>

\* Locked でない: 読んだ写しを持ち、
ReadHook(h) ==
  /\ ~Locked /\ h.pc = "pending"
  /\ hooks' = (hooks \ {h}) \cup {[h EXCEPT !.pc = "read", !.snap = work]}
  /\ UNCHANGED <<alive, prompts, stops, resumes, work, nextId, infers, inferring, isnap, humanSet, endedSet>>

\* 別のステップで写しを書く。
WriteHook(h) ==
  /\ ~Locked /\ h.pc = "read"
  /\ hooks' = hooks \ {h}
  /\ work' = Apply(h.kind, h.snap)
  /\ Marks(h.kind, h.snap)
  /\ UNCHANGED <<alive, prompts, stops, resumes, nextId, infers, inferring, isnap, humanSet>>

HookStep == \E h \in hooks : RunHook(h) \/ ReadHook(h) \/ WriteHook(h)

--------------------------------------------------------------------------------
\* companion (mod) の推定の題

\* turns が InferAfter 以上で推定の題が無い記録にだけ起きる。起こすたびに回数を数え、上限で止まる。
InferStart ==
  /\ alive /\ work.present /\ work.turns >= InferAfter /\ ~work.inferred
  /\ ~inferring /\ infers < MaxInfers
  /\ infers' = infers + 1
  /\ inferring' = TRUE
  /\ isnap' = work
  /\ UNCHANGED <<alive, prompts, stops, resumes, work, hooks, nextId, humanSet, endedSet>>

\* 推定が終わって `work set` で書く。Locked なら今の記録の inferred だけを書き換え、記録が消えていれば書かない。
\* Locked でなければ起きたときの写しに inferred を立てて書く。
InferWrite ==
  /\ inferring
  /\ inferring' = FALSE
  /\ work' = IF Locked THEN (IF work.present THEN [work EXCEPT !.inferred = TRUE] ELSE work)
             ELSE [isnap EXCEPT !.inferred = TRUE]
  /\ UNCHANGED <<alive, prompts, stops, resumes, hooks, nextId, infers, isnap, humanSet, endedSet>>

\* 推定が失敗して何も書かない (API error、nothing-to-fork)。回数は数えたまま。
InferFail ==
  /\ inferring
  /\ inferring' = FALSE
  /\ UNCHANGED <<alive, prompts, stops, resumes, work, hooks, nextId, infers, isnap, humanSet, endedSet>>

--------------------------------------------------------------------------------
\* 人と daemon

\* 人がアプリから subject を直す (`work set`、origin human)。lock の中の 1 ステップ。
Human ==
  /\ work.present /\ ~humanSet
  /\ work' = [work EXCEPT !.subject = "human"]
  /\ humanSet' = TRUE
  /\ UNCHANGED <<alive, prompts, stops, resumes, hooks, nextId, infers, inferring, isnap, endedSet>>

\* housekeeping が終了したセッションの記録を消す。hook が残っている間 (1 時間ごとの掃除に hook が跨ることは無い) は消さない。
Prune ==
  /\ ~alive /\ work.present /\ hooks = {} /\ ~inferring
  /\ work' = Absent
  /\ prompts' = 0 /\ stops' = 0
  /\ humanSet' = FALSE /\ endedSet' = FALSE
  /\ UNCHANGED <<alive, resumes, hooks, nextId, infers, inferring, isnap>>

Next ==
  \/ Prompt \/ Stop \/ Exit \/ Resume
  \/ HookStep
  \/ InferStart \/ InferWrite \/ InferFail
  \/ Human \/ Prune

\* hook と companion の書き込みはいずれ終わる。
Spec == Init /\ [][Next]_vars /\ WF_vars(HookStep) /\ WF_vars(InferWrite \/ InferFail)

--------------------------------------------------------------------------------
\* 不変条件

\* prompt を 1 度でも打ったセッションは、hook が動き終わった時点で記録を持つ (依存図から落ちない)
WorkAfterPrompt == (hooks = {} /\ prompts > 0) => work.present

\* 人が直した subject を hook も companion も書き換えない
HumanSubjectKept == (humanSet /\ work.present) => work.subject = "human"

\* SessionEnd が書いた ended を、遅れて書いた Stop や companion が消さない (消すのは resume の hook と prune だけ)
EndedStays == (endedSet /\ work.present) => work.ended

\* hook と companion が交錯しても turns の更新を失わない
NoLostUpdate == (hooks = {} /\ ~inferring /\ work.present) => work.turns = prompts

\* companion は推定の題がある記録に重ねて走らず、回数の上限を越えない
InferBounded == infers <= MaxInfers /\ ~(work.inferred /\ inferring /\ Locked)

\* 終了したセッションの記録は、生き返らなければいずれ ended になるか消える
EventuallyEnded == (~alive) ~> (work.ended \/ ~work.present \/ alive)

=============================================================================
