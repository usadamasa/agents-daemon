# パッケージ構造の lint と改名

`task lint` のうち、パッケージの分け方と依存の向きを見る検査 (go-arch-lint、analyze-modularity) の判定の仕方と、
パッケージを動かす・改名するときの手順。

## analyze-modularity の判定

`scripts/analyze-modularity.sh` は base の commit で測ったレポートと比べて、
`analyze-modularity --strict --quiet --baseline <base のレポート>` で判定する。base は CI では PR の base.sha、
手元では origin/main との merge-base になる。スタックした PR も main との merge-base で比べるので、
スタック全体の差分で判定される。

- 落ちるのは、揮発の Zone of Pain のパッケージ (具象で安定、直近 200 commit のうち 5 commit 以上が触っている) で、
  D が base より悪くなったときだけ。D を悪くしない変更なら、揮発のパッケージも避けずに触ってよい。
- 揮発の判定に使う commit 数は `analyze-modularity .` の JSON の `recent_commit_count` を読む。数えるのは
  `_test.go` でない `.go` を触った commit だけで、`git log` の件数より小さいことがある。
- 構造制約のパッケージ (Ce=0 かつ Ca>0。JSON の `structurally_constrained`) は commit 数に関係なく落ちない。
- パッケージの統合は呼び手の Ce を減らし、I を下げて D を悪くすることがある。統合を試す前に、両方を import している
  呼び手の Ce と D を `analyze-modularity .` の JSON で見る。

## go-arch-lint とテストの import

go-arch-lint は `_test.go` の import も依存として数える。component の境界を切り直す変更は、テストが import する先を
先に動かしてから行う (PR を分けるなら、テストの import を動かす PR を先に置く)。

## パッケージの改名と移動

パッケージ名は `gopls rename -w <doc.go>:<行>:<列> <新しい名前>` で変える (位置は package 節の名前の上)。
呼び手の識別子と import path を型を見て書き換える。gopls v0.23 はディレクトリの移動だけ失敗するので、
続けて `git mv` でディレクトリを動かし、import path の行を Edit で直す。動かした先が component の `in` に
含まれるかは AGENTS.md の「レイアウト」に従って確かめる。
