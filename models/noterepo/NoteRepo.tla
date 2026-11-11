---- MODULE NoteRepo ----
\* 依存図の単体ビューで、辺の note (edge add で書いた補足) の #N を補う owner/repo の決め方 (links.js の edgeNoteRepo、#551)。
\* 辺の両端、before の参照の状態を daemon が引けたか、note の番号のすべての組を初期状態に並べ、決めた repo を不変条件で
\* 確かめる。状態は遷移しない。
EXTENDS FiniteSets, Naturals

CONSTANTS
  Repos,     \* GitHub の owner/repo
  Nums,      \* issue / PR の番号
  Rule,      \* "match" (番号で合わせる)、"after" / "before" (辺の向きで選ぶ)、"matchPreferBefore" (両端が合えば before)、
             \* "matchViaState" (参照の repo を daemon が引いた状態から取る。引けない参照は repo が分からない)
  Fallback   \* 番号の合う参照が無いとき。"unanimous" (辺が触れる repo が 1 つならそれ)、"before" (before の端点の repo)

NoLink == "nolink"

\* 端点。home は端点の repo (辺を書いたセッションの場所を repos で引いた github_repo)。
\* 参照 (pr: / issue:) の端点は ref に参照の owner/repo、num に番号を持つ。セッションとタスクの端点は ref と num を使わない。
Endpoints == [kind : {"ref", "session"}, home : Repos, ref : Repos, num : Nums]

\* fetched は daemon が gh で before の参照の状態を引けたか (refstate)。引けない参照は gh の失敗、権限の無い repo、無い番号。
VARIABLES before, after, fetched, n, intent
vars == <<before, after, fetched, n, intent>>

\* Own は端点そのものが属する repo。参照なら参照の owner/repo、ほかは home。
Own(e) == IF e.kind = "ref" THEN e.ref ELSE e.home

\* Matches は番号が note の #k と同じ参照の端点。MatchRepos はその owner/repo。
Matches(k) == {e \in {before, after} : e.kind = "ref" /\ e.num = k}
MatchRepos(k) == {e.ref : e \in Matches(k)}

\* Touched は辺が触れる repo。書き手 (辺を書いたセッション) の repo は端点の home のどれかにある前提 (README の前提)。
Touched == {Own(before), Own(after), before.home, after.home}

\* note の書き手が #n で指したもの。端点の参照の番号と合えばその参照 (skill の例は「PR #<番号>」と参照の番号を書く)。
\* 合わなければ、辺が触れる repo のどれの issue かは分からないので、どれでもありうる。
Init ==
  /\ before \in Endpoints
  /\ after \in Endpoints
  /\ fetched \in BOOLEAN
  /\ n \in Nums
  /\ intent \in IF Matches(n) = {} THEN Touched ELSE MatchRepos(n)

Next == UNCHANGED vars
Spec == Init /\ [][Next]_vars

TheOne(S) == CHOOSE r \in S : TRUE

Unmatched ==
  CASE Fallback = "before" -> Own(before)
    [] Fallback = "unanimous" -> IF Cardinality(Touched) = 1 THEN TheOne(Touched) ELSE NoLink

\* match は fetched を読まない。参照の owner/repo と番号は ref の文字列から取れる。
Resolve ==
  CASE Rule = "after" -> after.home
    [] Rule = "before" -> Own(before)
    [] Rule = "match" ->
         IF Cardinality(MatchRepos(n)) = 1 THEN TheOne(MatchRepos(n))
         ELSE IF MatchRepos(n) = {} THEN Unmatched
         ELSE NoLink
    [] Rule = "matchViaState" ->
         IF before \in Matches(n) /\ ~fetched THEN NoLink
         ELSE IF Cardinality(MatchRepos(n)) = 1 THEN TheOne(MatchRepos(n))
         ELSE IF MatchRepos(n) = {} THEN Unmatched
         ELSE NoLink
    [] Rule = "matchPreferBefore" ->
         IF before \in Matches(n) THEN before.ref
         ELSE IF after \in Matches(n) THEN after.ref
         ELSE Unmatched

TypeOK ==
  /\ before \in Endpoints
  /\ after \in Endpoints
  /\ fetched \in BOOLEAN
  /\ n \in Nums
  /\ Resolve \in Repos \cup {NoLink}

\* 番号が合う参照が 1 つの repo に決まれば、その repo へリンクする (辺の向きと、状態を引けたかに依らない)。
RefNoteResolves ==
  Cardinality(MatchRepos(n)) = 1 => Resolve = TheOne(MatchRepos(n))

\* 指したものと違う repo へリンクしない (決められなければリンクにしない)。番号が合う参照のある note と無い note。
NoWrongLink ==
  Matches(n) # {} => Resolve \in {intent, NoLink}
NoWrongUnmatchedLink ==
  Matches(n) = {} => Resolve \in {intent, NoLink}

\* 番号が合う参照のある note がリンクにならないのは、両端が同じ番号で別 repo のときだけ。
MatchedNoteLinks ==
  (Matches(n) # {} /\ Resolve = NoLink) => Cardinality(MatchRepos(n)) > 1

\* 両端と参照がどれも同じ repo の辺 (同じ repo の session → pr、task → task) は、その repo のまま。
SameRepoStable ==
  \A r \in Repos :
    (before.home = r /\ after.home = r /\ Own(before) = r /\ Own(after) = r) => Resolve = r
====
