# PR とそれを出したセッションの辺のモデル (TLA+)

渡す側が `pr:P → task` だけを書いても、P の head branch で動くセッション A と P のノードがつながるよう、
daemon が `sid:A → pr:P` の辺を導く手順 (#487) と、A の branch を使い回す窓 (#577) と、人が消した組を導き直さない
記録 (#576) を TLA+ に写したもの。照合の相手 (どのセッションから導くか、#575) は別のモジュール `PRAuthor.tla` が持つ。
回し方と期待の突き合わせは `models/handoff/README.md` と同じ `task model-check`。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `PRLink.cfg` | `PRKept`: 写しが済んだ状態の P を掃除する tick の始めに導く条件が揃っていたら、P が A につながったまま残る | 導きを掃除より前に行い、写しが済んだ状態の P を照合するときは branch を引き直す。マージで `pr:P → task` が消えても `sid:A → pr:P` は残る |
| | `NoDaemonLoop`: daemon は自分の PR を待つ辺と輪になる辺を tick の終わりに残さない | `pr:P → task:A/x` があれば導かず、導いた後に書かれたら導いた辺を消す |
| | `LoopResolved` (liveness): 導いた辺の輪はいずれ解ける | 同上を tick ごとに行う |
| | `EventuallyAnchored` (liveness): A が P の branch で生きていて P が open の間に、人が「導かない」と記録していなければ、いずれ A とつながる | 写しが無いか open なら引き直して head branch を写し、使い回しの期限が切れたら branch を引き直して導く |
| | `NoRederive`: 人が `sid:A → pr:P` を消したら、人が同じ組を書き直すか記録が期限切れになるまで導き直さない | CLI の `edge rm` でもアプリの Unlink でも、辺を消すのと同じロックの中で組を記録し、導きは記録のある組を飛ばす。飛ばすたびに記録の保持期間を数え直す |
| `PRLinkEveryTick.cfg` | 同上が成り立つ | branch を tick ごとに引く基準の形 |
| `PRLinkStaleBranch.cfg` | `PRKept` が破れる (`violated` を期待) | #577 の修正前の形。branch を切り替えた直後の窓で初めて引いた P がマージ済みだと、古い branch で照合して導けず、同じ tick に `pr:P → task` が消える |
| `PRLinkRefetchFirst.cfg` | `PRKept` が破れる | P を初めて引いた tick と open から済んだ状態へ変わった tick だけ引き直す形。マージ済みの P へ後から `pr:P → task` を書くと、引き直さずに掃除する |
| `PRLinkHoldSweep.cfg` | `PRKept` が破れる | 済んだ候補の掃除を 1 tick 遅らせる形。使い回しの窓 (1 分) は tick (5 秒) より長く、遅らせた次の tick も古い branch で照合する |
| `PRLinkSweepFirst.cfg` | `PRKept` が破れる | 掃除の後に導くと、初めて引いた時点でマージ済みの PR は `pr:P → task` が消えて導く元が無くなる |
| `PRLinkNoSelfWait.cfg` | `NoDaemonLoop` が破れる | 自分の PR を待つ辺を見ずに導くと、A のタスクと P の間に待ちの輪を作る |
| `PRLinkRederive.cfg` | `NoRederive` が破れる | #576 の修正前の形。人が消した組を記録せず、次の tick で導き直す |
| `PRLinkDeclineApp.cfg` | `NoRederive` が破れる | アプリの Unlink だけ記録する形。CLI の `edge rm` で消した組を導き直す |
| `PRLinkHideDerived.cfg` | `NoRederive` が破れる | アプリが導いた辺に Unlink を出さない形。CLI の `edge rm` で消した組を導き直し、アプリからは外せない |
| `PRLinkSplitDecline.cfg` | `NoRederive` が破れる | 辺を消してからロックの外で記録する形。間の tick が導き直す |
| `PRLinkNoTouchDecline.cfg` | `NoRederive` が破れる | 記録で導きを飛ばしても保持期間を数え直さない形。導く条件が揃ったまま記録が期限切れになり、導き直す |
| `PRLinkRecord.cfg` | `PRLink.cfg` の性質がすべて成り立つ | A が P を出した記録で導く形 (#575)。A が終了していても別の branch にいても導くので、掃除の前に導く順序、自分の PR を待つ形、人が外した組の記録とそのまま両立する |

## 照合の相手 (PRAuthor)

A は P を出したセッション、B はほかのセッション。どちらも終了し、branch を移り (往復を含む)、B は P の branch に
いてもよい。tick ごとに生きているセッションの今の branch を引く形に縮め、branch の使い回しと掃除の順序は PRLink に
任せる。記録は A が P を出したときに書かれ、A の終了後の保持期間で消えうる (`Expire`)。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `PRAuthor.cfg` | `IssuerKept`: 記録が残っている間に P の辺が掃除されるなら、そのとき P は A につながっている | 記録があれば、A の終了・移動・同じ branch の B にかかわらず記録の持ち主へ導く |
| | `NoWrongAnchor`: 導く辺は A からだけ | 記録があれば branch で照合しない |
| | `EventuallyIssuer` (liveness): 記録があり P が open の間に、いずれ A とつながる | 同上を tick ごとに行う |
| `PRAuthorExpire.cfg` | `IssuerKept` と `EventuallyIssuer` が成り立つ | 記録が保持期間で消えても、消えるまでに掃除される P は A につながっている |
| `PRAuthorExpireWrong.cfg` | `NoWrongAnchor` が破れる (受け入れた限界) | A の終了から保持期間が過ぎて記録が消えた後に初めて候補になった P は、branch の照合で B へ導きうる |
| `PRAuthorUnrecorded.cfg` | `NoWrongAnchor` が破れる (受け入れた限界) | 記録の無い P (web や `gh stack` で作った PR) は #575 の修正前と同じ branch の照合になり、A が移った後に同じ branch にいる B へ導きうる |
| `PRAuthorLiveAlone.cfg` | `IssuerKept` が破れる | #575 の修正前の形 (生きているセッションの branch だけで照合)。A が終了するか別の branch へ移った後に P がマージされると、導けないまま辺が掃除される |
| `PRAuthorLiveShared.cfg` | `NoWrongAnchor` が破れる | #575 の修正前の形。A が移った後、P の branch にいるだけの B へ導く。A と B が同じ branch にいる間は曖昧で導かない |
| `PRAuthorHistory.cfg` | `IssuerKept` が破れる | 終了したセッションも最後の hook のときの branch で照合する形 (issue の候補 1)。A が移った後は生きているので今の branch で照合され、外れる |
| `PRAuthorNearest.cfg` | `NoWrongAnchor` が破れる | 合うセッションが複数なら 1 つを選ぶ形 (issue の候補 3)。時刻の近さは出した者を決めないので、B を選びうる |
| `PRAuthorRecordOnly.cfg` | どの性質も成り立つ | 記録だけで導く形。代わりに記録の無い PR は導かない |

採ったのは `PRAuthor.cfg` の形 (記録を優先し、記録が無いときだけ branch で照合)。記録の無い PR で #575 の修正前の
挙動を変えないため、`NoWrongAnchor` は記録のある PR についてだけ持つ。

`PRKept` は、写しが open の間に導く条件が揃ったことを数えない。使い回しの窓がある限り open の P は導くのが遅れ、
遅れは性質の破れではない (導くことは `EventuallyAnchored` が持つ)。掃除で `pr:P → task` が消える tick が、
P を A につなげる最後の機会になる。

元の `EventuallyAnchored` (前件に記録の有無を持たない形) は `NoRederive` と両立しない。人が消した後も導く条件が
揃ったままなら、導かないことが liveness の反例になる。人の判断を導きより優先し、記録のある組を前件から外した。

## 実装との対応

| モデルの動作 | 実装 |
| ---- | ---- |
| `Tick` | `refpoll` の `Poller.Refresh` (引き直し、`anchor`、`sweep` の順) |
| `Tick` の `cand` | `Poller.candidates` |
| `Tick` の `look` と `reread` (`Locate = "done"`) | `Poller.anchor` が候補ごとに呼ぶ `Poller.place`。写しが済んだ状態 (`refstate.Done`) の候補では `locateTTL` を見ずに引き直す |
| `Tick` の `CanAnchor` | `edge` の `Store.Anchor` (edges/.lock の中で辺の条件を確かめ直す) |
| `Tick` の `s2` | `edge` の `Store.Unanchor` |
| `Tick` の `swept` | `Poller.sweep` |
| `Expire` | `locateTTL` (1 分) が過ぎる |
| `OtherLocate` | ほかの候補の PR を照合するために `Poller.place` が同じ cwd を引く |
| `Tick` の `CanAnchor` の `~declined` | `Store.Anchor` が `edges/<id>.declined` を見て導きを飛ばし、mtime を進める (`TouchDecline`) |
| `RemoveSidPr("cli")` / `RemoveSidPr("app")` | `edge rm` (`Store.Remove`)。アプリの Unlink も同じ `edge rm` を呼ぶ。`sid:` → `pr:` の辺を消したら同じロックの中で `edges/<id>.declined` を書く |
| `AddManual` | `edge add` (`Store.Add`)。書いた組の `edges/<id>.declined` を消す |
| `ForgetDecline` | housekeeping の `Store.Prune` が `Retention` (7 日) を過ぎた記録を mtime で消す |
| `ByRecord` / PRAuthor の `rec` | `worklog` の `Store.Ingest` が Bash の PostToolUse で `gh pr create` の stdout の PR の URL を `prs` に足す。daemon の `prIssuers` が作業の記録から PR ごとに引く |
| PRAuthor の `Targets` (`Match = "record"`) | `Poller.issuer`。記録のあるセッションがちょうど 1 つならそれ、無ければ branch の合う生きているセッションがちょうど 1 つのときだけ |
| PRAuthor の `Forget` | housekeeping の `worklog` の `Store.Prune` が終了から `worklog.Retention` (7 日) を過ぎた作業の記録を消す |

同じ性質を実装に対してテストが持つ。

| 性質 | テスト |
| ---- | ---- |
| `PRKept` | `refpoll/refpoll_test.go` の `TestRefresh_マージ済みのPRも掃除の前に導く` と `TestRefresh_branchを切り替えた直後に初めて引いたマージ済みのPRも導く` |
| `NoDaemonLoop` / `LoopResolved` | `edge/anchor_test.go` の `TestAnchor_自分のPRを待つセッションには導かない` と `TestAnchor_自分のPRを待つ形になったら導いた辺を消す` |
| `EventuallyAnchored` | `refpoll/refpoll_test.go` の `TestRefresh_PRのbranchのセッションからPRへ辺を導く` |
| `IssuerKept` / `NoWrongAnchor` | `refpoll/refpoll_test.go` の `TestRefresh_PRを出した記録のセッションへ導く`、`worklog/worklog_test.go` の `TestIngest_ghprcreateが出したPRを記録に残す` と `TestIngest_ghprcreateでなければPRを残さない` |
| `NoRederive` | `edge/decline_test.go` の `TestAnchor_人が消した組には導き直さない`、`TestAdd_人が書き直したら導かないの記録を外す`、`TestPrune_導かないの記録は飛ばした回から保持期間で消える` |

## 前提

- gh はいつも成功し、写しは保持期間で消えない (`models/refstate` が持つ)。
- 生きているセッションのうち repo と branch の合うものが 2 つ以上なら導かない。PRLink は合うセッションを A の 1 つに
  絞り、複数のセッションは PRAuthor が持つ。
- PR を出した記録は P の作成と同時にある (PostToolUse は `gh pr create` の直後に来て、渡す側が P を知るより前)。
  hook の失敗と、`gh pr create` 以外の作り方 (web、`gh stack`、GitHub の MCP) は「記録の無い PR」にまとめる。
  同じ PR を 2 つのセッションが記録していたら記録で決めず、branch で照合する。
- subagent の中で打った `gh pr create` の hook の `session_id` が親のセッションになるかは docs に書かれておらず、
  確かめていない。
- 導き、導いた辺の除去、`edge add` / `edge rm` は `edges/.lock` の中で読んで書くので、交錯は 1 ステップで足りる
  (`models/handoff` の `EdgeBind` と同じ)。
- 人が `sid:A → pr:P` を消すのは、同じ名前の branch を別の作業で使い回したような取り違えを外すとき。導いた辺でも
  人が書いた辺でも組を記録する。daemon 自身の削除 (導いた辺の除去、済んだ参照の掃除、`task dismiss`) は記録しない。
- 記録の保持期間は時刻ではなく、いつ起きてもよい `ForgetDecline` で表す。導く条件が途切れて 7 日経つと記録が消え、
  条件が揃い直せば導く。モデルは条件が揃っている間に消えないことだけを見る。
- 使い回しの期限は時刻ではなく、いつ起きてもよい `Expire` で表す。期限の内の tick の数は限らない。
- P が open の間に A が P の branch へ切り替え、期限が切れる前に別の branch へ戻ってからマージされると、記録の無い P
  は導かない。daemon が A を P の branch で見たことが無いため。A が `gh pr create` で出した P は記録で導く。
- A の cwd は作業の記録の cwd (hook が渡す今の cwd)。記録が無ければ pane の cwd (起動したディレクトリ) を使う。
