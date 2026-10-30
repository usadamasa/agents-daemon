---------------------------- MODULE EdgeBind ----------------------------
(* セッションをまたぐ辺 1 本 (edges/<id>.json) の session:<名前> の端点を、daemon が session ID に結びつける手順を
   写したモデル。結びつけ (edge の Bind) と、CLI の `edge add` / `edge rm` (Add / Remove) が同じファイルを
   交錯して書く。

   - 辺の id は書いたときの両端の参照 (結びつける前の session:<名前>) から決まり、結びつけても変わらない。同じ参照の組の
     `edge add` と `edge rm` は同じファイルを指す。
   - ファイルの中身は端点の今の値で、Name は結びついていない session:<名前>、Sessions の要素は結びついた sid:<id>。
   - daemon は tick ごとに台帳に現れたセッションを見て、結びついていない端点を 1 つのセッションに結びつける。どの
     セッションを選ぶか (台帳の first_seen と辺の created_at) はここでは非決定にし、現れたセッションのどれでも選べる
     ことにして広く探す。
   - `edge add` は辺が無ければ結びついていない端点で作り、あれば端点を残して note と cwd だけを書き直す。
   - `edge rm` はファイルを消す。消した辺を後から `edge add` で書き直すのは新しい辺で、もう一度結びついてよい。
   - Locked が TRUE なら、Add / Remove / Bind は edges/.lock を取り、読んでから書くまでを 1 ステップにする。FALSE なら
     読むのと書くのが別のステップになる (os.Remove は 1 ステップのまま)。
   - ファイルの書き込みは statefile.WriteJSONAtomic なので 1 回の書き込みを 1 ステップにしている。 *)
EXTENDS Naturals

CONSTANTS
  Sessions,  \* 台帳に現れうるセッション (sid:<id>)
  MaxAdds,   \* `edge add` を走らせる回数
  MaxRms,    \* `edge rm` を走らせる回数
  Locked     \* Add / Remove / Bind を edges/.lock の中で行うか

None == "none"  \* ファイルが無い
Name == "name"  \* 結びついていない session:<名前>

VARIABLES
  file,    \* ファイルの中身: None、Name、Sessions の要素
  origin,  \* ファイルを無い状態から作った主体 ("add" / "bind")。ファイルが無ければ None
  bound,   \* 今のファイル (作り直すまで) の端点を最初に結びつけたセッション。まだなら None
  seen,    \* 台帳に現れたセッション
  adds,    \* 走らせた `edge add` の数
  rms,     \* 走らせた `edge rm` の数
  apc,     \* `edge add` の段: idle / read
  aread,   \* `edge add` が読んだファイルの中身
  bpc,     \* 結びつけの段: idle / read
  bto      \* 結びつけが選んだセッション

vars == <<file, origin, bound, seen, adds, rms, apc, aread, bpc, bto>>

Values == {None, Name} \cup Sessions

TypeOK ==
  /\ file \in Values
  /\ origin \in {None, "add", "bind"}
  /\ bound \in {None} \cup Sessions
  /\ seen \subseteq Sessions
  /\ adds \in 0..MaxAdds
  /\ rms \in 0..MaxRms
  /\ apc \in {"idle", "read"}
  /\ aread \in Values
  /\ bpc \in {"idle", "read"}
  /\ bto \in {None} \cup Sessions

\* 渡す側が `edge add` で書いた辺が 1 本ある状態から始める。
Init ==
  /\ file = Name
  /\ origin = "add"
  /\ bound = None
  /\ seen = {}
  /\ adds = 0
  /\ rms = 0
  /\ apc = "idle"
  /\ aread = None
  /\ bpc = "idle"
  /\ bto = None

\* v を書く。無い状態から作るなら作った主体を残し、結びつけの記録を空にする (新しい辺)。
Write(v, who) ==
  /\ file' = v
  /\ origin' = IF file = None THEN who ELSE origin
  /\ bound' = IF file = None THEN (IF v \in Sessions THEN v ELSE None)
              ELSE IF bound = None /\ v \in Sessions THEN v ELSE bound

\* statusline と daemon の tick でセッションが台帳に現れる (daemon/ledger の Observe)。
Appear(s) ==
  /\ s \notin seen
  /\ seen' = seen \cup {s}
  /\ UNCHANGED <<file, origin, bound, adds, rms, apc, aread, bpc, bto>>

\* Locked: lock の中で読んで書く。辺があれば端点を残し、無ければ結びついていない端点で作る。
AddLocked ==
  /\ Locked
  /\ apc = "idle"
  /\ adds < MaxAdds
  /\ Write(IF file = None THEN Name ELSE file, "add")
  /\ adds' = adds + 1
  /\ UNCHANGED <<seen, rms, apc, aread, bpc, bto>>

\* Locked でない: 読む。
AddRead ==
  /\ ~Locked
  /\ apc = "idle"
  /\ adds < MaxAdds
  /\ aread' = file
  /\ apc' = "read"
  /\ UNCHANGED <<file, origin, bound, seen, adds, rms, bpc, bto>>

\* Locked でない: 読んだ中身から書く。読んだ後に結びついていても、読んだ値で上書きする。
AddWrite ==
  /\ apc = "read"
  /\ Write(IF aread = None THEN Name ELSE aread, "add")
  /\ adds' = adds + 1
  /\ apc' = "idle"
  /\ aread' = None
  /\ UNCHANGED <<seen, rms, bpc, bto>>

\* `edge rm`: ファイルを消す (os.Remove は 1 ステップ)。
Remove ==
  /\ rms < MaxRms
  /\ file' = None
  /\ origin' = None
  /\ bound' = None
  /\ rms' = rms + 1
  /\ UNCHANGED <<seen, adds, apc, aread, bpc, bto>>

\* Locked: lock の中で、結びついていない端点を現れたセッションに結びつける。
BindLocked(s) ==
  /\ Locked
  /\ bpc = "idle"
  /\ file = Name
  /\ s \in seen
  /\ Write(s, "bind")
  /\ UNCHANGED <<seen, adds, rms, apc, aread, bpc, bto>>

\* Locked でない: 辺を読み、結びついていなければ結びつけるセッションを選ぶ。
BindRead(s) ==
  /\ ~Locked
  /\ bpc = "idle"
  /\ file = Name
  /\ s \in seen
  /\ bto' = s
  /\ bpc' = "read"
  /\ UNCHANGED <<file, origin, bound, seen, adds, rms, apc, aread>>

\* Locked でない: 選んだセッションで書く。読んだ後に消されていても書く。
BindWrite ==
  /\ bpc = "read"
  /\ Write(bto, "bind")
  /\ bpc' = "idle"
  /\ bto' = None
  /\ UNCHANGED <<seen, adds, rms, apc, aread>>

\* 走らせる操作が尽きた後も止まらないようにする (TLC の deadlock を避ける)。
Idle == UNCHANGED vars

Next ==
  \/ \E s \in Sessions : Appear(s) \/ BindLocked(s) \/ BindRead(s)
  \/ AddLocked \/ AddRead \/ AddWrite
  \/ Remove
  \/ BindWrite
  \/ Idle

Spec == Init /\ [][Next]_vars

\* 結びついた端点は、辺が消されるまで別の値 (結びついていない名前や別のセッション) に戻らない。
BindOnce == file # None /\ bound # None => file = bound

\* `edge rm` で消した辺を結びつけの書き込みが作り直さない。ファイルを無い状態から作るのは `edge add` だけ。
NoResurrect == file # None => origin = "add"

=============================================================================
