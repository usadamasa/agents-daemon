---
name: verifying-agent-tasks-gui
description: >-
  依存図のアプリ agent-tasks (`gui/`) の見た目や配置を変えた PR を、マージする前に本物のアプリで確かめるときに使う。
  fixture の DOM テストだけでは判定できない線の濃さ、色の見分け、枠と辺の重なりを見るときにも引く。
---

# verifying-agent-tasks-gui

依存図のアプリを変えたブランチを手元のアプリに建て、試しの辺で図の形を作って利用者に見てもらい、
片付けてからマージする。`gui/frontend/test/ui.test.js` は配置と class を確かめるが、見た目 (線の濃さ、色の見分け、
見出しの重なり、縮小したときの見え方) は判定できない。

## 1. ブランチのアプリを建てて開く

`~/Applications` への書き込みと `open` は sandbox が止めるので、利用者に `!` で打ってもらう。

```sh
! task gui:install
! open ~/Applications/agent-tasks.app
```

- worktree の中で打つと、その worktree の `gui/` から建てる。
- 開いているアプリは閉じてから開き直す。窓の題のバージョンは `plugin.json` のままで、ブランチの中身かどうかは題では分からない。
- マージしてリリースした後は `agents-daemon update` が建て直すので、元に戻す手順は要らない。

fixture の図を HTML で見るだけなら、ブラウザ無しで `task gui:snapshot` が書き出す。

## 2. 試しの辺で図の形を作る

変更が効く形を、試しのタスクと辺で作る。辺の書き方は agents-daemon:peer-sessions skill の「依存の辺を書く」。
相手のタスクは相手のセッションにしか作れないので、peer を `agents-daemon spawn` で起こし、pending のタスクを 1 つ作って
`task_id` と `session_id` を `SendMessage` で返すよう頼む。定型の作業なので `"model": "sonnet"` で起こす。

| 見たい形 | 作り方 |
| ---- | ---- |
| window をまたぐ鎖 | 自分のタスク → タブに起こした peer のタスク → 自分のタスク |
| rex session をまたぐ辺 | `"new_session": true` で起こした peer のタスク → 自分のタスク |
| 枠の外の参照 | `pr:<owner>/<repo>#<番号>` → 自分のタスク |
| 辺の無いノードの引き入れ | 辺を張らない peer のタスクを残す |
| 終わったノード | 完了したタスクを持つ端を作り、アプリの「Show completed」を入れる |
| rex session ごとの枠の色 | `new_session` の peer を、`gui/frontend/src/column.js` の `TONES` の数まで足す |

端はタスク (`task:<session_id>/<id>`) か参照で指す。`sid:` から `sid:` への辺は、daemon が before のセッションの
タスクをそのセッションのカードへ集め、after のセッションの頭のタスクへ配る形に組み直す。張った形と違う図になり、
ほかの試しの辺と組むと待ちの輪 (カードの `⟳`) ができて、輪の線がカードの中心どうしを斜めに結ぶ。

## 3. 見てもらう

利用者に画面を撮って貼ってもらう (自分で撮るなら cleanshot-capture skill)。次を見る。

- 枠に入るべきカードが、その rex session の枠と window の小枠の中にある
- 線が、要らないところで枠の境界を長く横切っていない
- 枠の見出しが、枠の線やカードと重ならずに読める
- 全体表示で縮小しても、枠の線と色が見分けられる
- 図が消えていない (配置が NaN になると図ごと描かれない)

直すところが出たら、テストを先に直してから実装を直し (dev-workflow:test-driven-development)、
1 から建て直して見てもらう。

## 4. 片付けてからマージする

1. 試しの辺を消す。`agents-daemon edge list --cwd <repo>` で一覧し、張ったときと同じ端の組を `agents-daemon edge rm` に渡す。
2. 試しのタスクを消す。自分のものは TaskUpdate で `deleted` にし、peer のものは peer に消してもらう。消さずに peer を畳むと、
   終了したセッションのタスクとして右の Ended の列に残る。
3. peer を畳む。タブは `rex window close -s <rex_session_id> <window_id>`、`new_session` で起こしたものは
   `agents-daemon spawned-session close <rex_session_id>` で閉じる。
4. 利用者がよしとしたら、PR の本文に見た形を書き足し、マージする。
