# 辺の note の番号を補う repo のモデル (TLA+)

依存図の単体ビューで、辺の note (`edge add` で書いた補足) の `#N` を補う owner/repo の決め方
(#551、`links.js` の `edgeNoteRepo`) を TLA+ に写したもの。辺の両端 (参照かセッションか、端点の repo、参照の
owner/repo と番号)、before の参照の状態を daemon が引けたか、note の番号の組をすべて初期状態に並べ、決めた repo を
不変条件で確かめる。回し方は `models/handoff/README.md` と同じ `task model-check`。

決め方は辺の向きに依らない。

- 端点の参照 (`pr:` / `issue:`) の番号が `#N` と合えば、その参照の owner/repo で補う。両端の参照が同じ番号で別の repo
  なら決められないので、リンクにしない。
- 合う参照が無ければ、辺が触れる repo (端点の repo と参照の owner/repo) が 1 つのときだけ補い、ほかはリンクにしない。
- 参照の owner/repo は ref の文字列から取り、daemon が状態を引けたかに依らない。

| cfg | 性質 | 成り立つ理由 / 破れる理由 |
| ---- | ---- | ---- |
| `NoteRepo.cfg` | `RefNoteResolves`: 番号が合う参照が 1 つの repo に決まれば、その repo で補う | 番号で合わせ、向きと状態を見ない |
| | `NoWrongLink` / `NoWrongUnmatchedLink`: 番号の合う参照があってもなくても、指したものと違う repo で補わない | 決められないときはリンクにしない |
| | `MatchedNoteLinks`: 番号の合う参照のある note がリンクにならないのは、両端が同じ番号で別 repo のときだけ | 番号の合う参照の repo を先に見る |
| | `SameRepoStable`: 両端と参照がどれも同じ repo の辺は、その repo で補う | どの枝もその repo を返す |
| `NoteRepoAfter.cfg` | `RefNoteResolves` が破れる (`violated` を期待) | after の端点の repo で補うと、別 repo の PR を待つ辺で `#N` が after の repo を指す (#551 の症状) |
| `NoteRepoBefore.cfg` | `RefNoteResolves` が破れる (`violated` を期待) | before の端点で補うと、after の参照の番号を書いた note (skill の session → pr の例) が before の repo を指す |
| `NoteRepoPreferBefore.cfg` | `NoWrongLink` が破れる (`violated` を期待) | 両端の参照が同じ番号で別 repo のとき before を選ぶと、after を指した note を before の repo で補う |
| `NoteRepoFallbackBefore.cfg` | `NoWrongUnmatchedLink` が破れる (`violated` を期待) | 番号の合わない `#N` を before の端点の repo で補うと、書き手の repo の issue を指した note を別 repo へ送る |
| `NoteRepoViaState.cfg` | `RefNoteResolves` が破れる (`violated` を期待) | 参照の repo を daemon が引いた状態から取ると、状態を引けない PR の番号を書いた note がリンクにならない |

同じ性質を実装に対して `gui/frontend/test/links.test.js` の `edgeNoteRepo` のテストが持つ。

## 前提

- note は `edge add` で書いた補足だけ。daemon が引いた issue / PR の題は参照のノードの題に出し、note に混ぜない
  (`deps/graph_test.go` の `TestGraph_refStates`)。
- 書き手は、端点の参照の番号を書くとき、その参照を指す (skill の例は「PR #<番号>」)。違う repo の同じ番号を指すことはない。
- 書き手 (辺を書いたセッション) の repo は端点の repo のどれかにある。番号の合わない `#N` は、辺が触れる repo のどれの
  issue でもありうる。
