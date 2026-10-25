---------------------------- MODULE FileNotice ----------------------------
(* ファイルを触ったセッションの model へ、そのファイルに関係するパーキングのメモを伝える手順を写したモデル (#89)。

   - メモは別のセッションが置く・まとめる (park add、internal/parking の Add)。置かれたメモは手渡しや捨てるで
     open でなくなる。
   - mod はボードを読み直して、この repo の open のメモの写し (parked の atom、hooks/board-data.ts の loadBoard) を持つ。
     写しは読み直すまで古いまま。
   - model が Read / Edit / Write を呼ぶと、tool が走った後の tool.call の hook (hooks/notice.ts の claim) が
     写しのうち触ったファイルを files に持つメモを選び、まだ伝えていないものを結果の context に添える。
     伝えたメモの id は told の atom に置く ($.state なので mod の読み直しでも残る)。
   - このセッションだけが置いたメモは model が自分で置いたものなので伝えない。
   - 同じターンで並列に呼ばれた tool の hook は交錯する。Atomic が TRUE なら、伝えるメモを選ぶのと told に足すのを
     1 回の update の中で行う (選ぶ → 足すが 1 ステップ)。FALSE なら told を読んでから別のステップで書く。
   - compact は model の context を要約に置き換える。添えたメモが要約に残るかは決まらないので、残らない側
     (model が知っているメモが空になる) に倒す。ResetOnCompact が TRUE なら compact で told も空にする。
     FALSE (今の hooks/notice.ts) なら told は残る。 *)
EXTENDS Naturals, FiniteSets

CONSTANTS
  Memos,          \* メモの id
  Files,          \* repo のファイル
  MemoFiles,      \* メモの files ([Memos -> SUBSET Files])
  Self,           \* mod が動いているセッション
  Others,         \* ほかのセッション
  Calls,          \* model の tool の呼び出し (1 つの id は 1 回だけ使う)
  Atomic,         \* 選ぶのと told に足すのを 1 回の update で行うか
  ResetOnCompact  \* compact で told を空にするか

Sessions == {Self} \cup Others

\* cfg が MemoFiles に当てる値。1 つのメモは全部のファイルを、ほかは 1 つのファイルだけを持つ。
MemoFilesMC == LET all == CHOOSE m \in Memos : TRUE
                   one == CHOOSE f \in Files : TRUE
               IN [m \in Memos |-> IF m = all THEN Files ELSE {one}]

VARIABLES
  open,     \* open のメモ
  placers,  \* メモを置いたセッション (park add の sources)
  snap,     \* mod の写し。メモごとの placers で、写しに無いメモは {}
  phase,    \* 呼び出しの段: idle / ran / read / done
  file,     \* 呼び出しが触ったファイル
  pending,  \* Atomic が FALSE のとき、told を読んで選んだメモ
  seen,     \* hook が写しを見た時点で、触ったファイルに関係したメモ
  told,     \* 伝えたメモ (told の atom)
  said,     \* 今の context でメモを添えた回数
  known,    \* model が今の context で知っているメモ
  after     \* 最後の compact より後に context を添えた (添えるメモが無かったものも含む) 呼び出し

vars == <<open, placers, snap, phase, file, pending, seen, told, said, known, after>>

TypeOK ==
  /\ open \subseteq Memos
  /\ placers \in [Memos -> SUBSET Sessions]
  /\ snap \in [Memos -> SUBSET Sessions]
  /\ phase \in [Calls -> {"idle", "ran", "read", "done"}]
  /\ file \in [Calls -> Files]
  /\ pending \in [Calls -> SUBSET Memos]
  /\ seen \in [Calls -> SUBSET Memos]
  /\ told \subseteq Memos
  /\ said \in [Memos -> Nat]
  /\ known \subseteq Memos
  /\ after \subseteq Calls

Init ==
  /\ open = {}
  /\ placers = [m \in Memos |-> {}]
  /\ snap = [m \in Memos |-> {}]
  /\ phase = [c \in Calls |-> "idle"]
  /\ file = [c \in Calls |-> CHOOSE f \in Files : TRUE]
  /\ pending = [c \in Calls |-> {}]
  /\ seen = [c \in Calls |-> {}]
  /\ told = {}
  /\ said = [m \in Memos |-> 0]
  /\ known = {}
  /\ after = {}

\* 写しのうち、f を files に持ち、このセッション以外も置いたメモ。
Relevant(f) == {m \in Memos : snap[m] # {} /\ f \in MemoFiles[m] /\ snap[m] # {Self}}

\* どこかのセッションがメモを置く。同じメモが open なら置いたセッションを足す (internal/parking の Add がまとめる)。
Place(m, s) ==
  /\ open' = open \cup {m}
  /\ placers' = [placers EXCEPT ![m] = IF m \in open THEN @ \cup {s} ELSE {s}]
  /\ UNCHANGED <<snap, phase, file, pending, seen, told, said, known, after>>

\* メモが手渡されるか捨てられて open でなくなる。
Close(m) ==
  /\ m \in open
  /\ open' = open \ {m}
  /\ UNCHANGED <<placers, snap, phase, file, pending, seen, told, said, known, after>>

\* mod がボードを読み直して写しを置き直す (hooks/board-data.ts の loadBoard)。
Reload ==
  /\ snap' = [m \in Memos |-> IF m \in open THEN placers[m] ELSE {}]
  /\ UNCHANGED <<open, placers, phase, file, pending, seen, told, said, known, after>>

\* model の tool が f を読む・書く。hook は tool が走った後に動く。
Touch(c, f) ==
  /\ phase[c] = "idle"
  /\ phase' = [phase EXCEPT ![c] = "ran"]
  /\ file' = [file EXCEPT ![c] = f]
  /\ UNCHANGED <<open, placers, snap, pending, seen, told, said, known, after>>

Say(ms) == [m \in Memos |-> IF m \in ms THEN said[m] + 1 ELSE said[m]]

\* Atomic: update(told, prev => ...) の中で、prev に無いメモを選んで足す。選んだメモを context に添える。
Claim(c) ==
  /\ Atomic
  /\ phase[c] = "ran"
  /\ LET new == Relevant(file[c]) \ told IN
       /\ told' = told \cup new
       /\ said' = Say(new)
       /\ known' = known \cup new
  /\ seen' = [seen EXCEPT ![c] = Relevant(file[c])]
  /\ phase' = [phase EXCEPT ![c] = "done"]
  /\ after' = after \cup {c}
  /\ UNCHANGED <<open, placers, snap, file, pending>>

\* Atomic でない: told を読んで選ぶ。
ReadTold(c) ==
  /\ ~Atomic
  /\ phase[c] = "ran"
  /\ pending' = [pending EXCEPT ![c] = Relevant(file[c]) \ told]
  /\ seen' = [seen EXCEPT ![c] = Relevant(file[c])]
  /\ phase' = [phase EXCEPT ![c] = "read"]
  /\ UNCHANGED <<open, placers, snap, file, told, said, known, after>>

\* Atomic でない: 選んだメモを told に足して context に添える。
WriteTold(c) ==
  /\ phase[c] = "read"
  /\ told' = told \cup pending[c]
  /\ said' = Say(pending[c])
  /\ known' = known \cup pending[c]
  /\ phase' = [phase EXCEPT ![c] = "done"]
  /\ after' = after \cup {c}
  /\ UNCHANGED <<open, placers, snap, file, pending, seen>>

\* Claude Code が context を compact する。model は添えられたメモを忘れる。
Compact ==
  /\ known' = {}
  /\ said' = [m \in Memos |-> 0]
  /\ after' = {}
  /\ told' = IF ResetOnCompact THEN {} ELSE told
  /\ UNCHANGED <<open, placers, snap, phase, file, pending, seen>>

Next ==
  \/ \E m \in Memos, s \in Sessions : Place(m, s)
  \/ \E m \in Memos : Close(m)
  \/ Reload
  \/ \E c \in Calls, f \in Files : Touch(c, f)
  \/ \E c \in Calls : Claim(c) \/ ReadTold(c) \/ WriteTold(c)
  \/ Compact

Spec == Init /\ [][Next]_vars

\* 1 つのメモを添えるのは 1 つの context の中で高々 1 回 (並列の Read で同じメモを 2 回添えない)
AtMostOnce == \A m \in Memos : said[m] <= 1

\* 触ったファイルに関係するメモは、hook が済んだ時点で told にある (伝え漏れが無い)。
\* 関係するかは hook が写しを見た時点 (seen) で測る。後から置かれたメモは次に触ったときに伝える。
\* compact で told を空にする形では、compact より前に済んだ呼び出しは数えない。
NoneMissed ==
  \A c \in Calls : phase[c] = "done" /\ (~ResetOnCompact \/ c \in after) => seen[c] \subseteq told

\* 最後の compact より後に触ったファイルに関係するメモは、model が今の context で知っている。
Informed == \A c \in after : phase[c] = "done" => seen[c] \subseteq known

=============================================================================
