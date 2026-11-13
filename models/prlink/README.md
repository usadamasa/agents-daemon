# PR とそれを出したセッションの辺のモデル (TLA+)

渡す側が `pr:P → task` だけを書いても、P の head branch で動くセッション A と P のノードがつながるよう、
daemon が `sid:A → pr:P` の辺を導く手順 (#487) を TLA+ に写したもの。回し方と期待の突き合わせは
`models/handoff/README.md` と同じ `task model-check`。ステップと実装の対応は `.tla` のコメントが持つ。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `PRLink.cfg` | `PRKept`: 導く条件が揃った tick の後は、P が A につながったまま残る | 導きを掃除より前に行う。マージで `pr:P → task` が消えても `sid:A → pr:P` は残る |
| | `NoDaemonLoop`: daemon は自分の PR を待つ辺と輪になる辺を tick の終わりに残さない | `pr:P → task:A/x` があれば導かず、導いた後に書かれたら導いた辺を消す |
| | `LoopResolved` (liveness): 導いた辺の輪はいずれ解ける | 同上を tick ごとに行う |
| | `EventuallyAnchored` (liveness): A が P の branch で生きていて P が open の間に、いずれ A とつながる | 写しが無いか open なら引き直して head branch を写し、tick ごとに導く |
| `PRLinkSweepFirst.cfg` | `PRKept` が破れる (`violated` を期待) | 掃除の後に導くと、初めて引いた時点でマージ済みの PR は `pr:P → task` が消えて導く元が無くなる |
| `PRLinkNoSelfWait.cfg` | `NoDaemonLoop` が破れる (`violated` を期待) | 自分の PR を待つ辺を見ずに導くと、A のタスクと P の間に待ちの輪を作る |

同じ性質を実装に対してテストが持つ。

| 性質 | テスト |
| ---- | ---- |
| `PRKept` | `refpoll/refpoll_test.go` の `TestRefresh_マージ済みのPRも掃除の前に導く` |
| `NoDaemonLoop` / `LoopResolved` | `edge/anchor_test.go` の `TestAnchor_自分のPRを待つセッションには導かない` と `TestAnchor_自分のPRを待つ形になったら導いた辺を消す` |
| `EventuallyAnchored` | `refpoll/refpoll_test.go` の `TestRefresh_PRのbranchのセッションからPRへ辺を導く` |

## 前提

- gh はいつも成功し、写しは保持期間で消えない (`models/refstate` が持つ)。
- 生きているセッションのうち repo と branch の合うものが 2 つ以上なら導かない。モデルは合うセッションを A の 1 つに絞る。
- 導き、導いた辺の除去、`edge add` / `edge rm` は `edges/.lock` の中で読んで書くので、交錯は 1 ステップで足りる
  (`models/handoff` の `EdgeBind` と同じ)。
- 導いた辺を人が `edge rm` すると (アプリの Unlink も)、条件が揃っていれば次の tick で導き直す。A が P を出した事実は
  変わらないため。
- モデルは A の branch を tick ごとに読むが、実装は cwd の repo と branch を `locateTTL` (1 分) のあいだ使い回す。
  open の PR では導くのが遅れるだけで性質は変わらない。branch を切り替えた直後に初めて引いた PR が既にマージ済みだと、
  古い branch のまま `pr:P → task` が消え、導く元が無くなる。
- A の cwd は作業の記録の cwd (hook が渡す今の cwd)。記録が無ければ pane の cwd (起動したディレクトリ) を使う。
