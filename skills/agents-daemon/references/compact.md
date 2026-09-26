# context 閾値からの自動 compact

画面の読み取り元と送信の作法は [pane-io.md](pane-io.md)､毎 tick の判定表は
[architecture.md](architecture.md) にある｡

context 使用率が閾値を超えたら､ユーザーの操作なしに
「compact-prep で復旧材料を書く」→「`/compact` を実行する」→「作業を再開する」まで
走らせる｡3 段に分かれ､いずれも**画面の文字列を判定に使わない**｡使うのは別プロセスが
書いたファイルの有無と mtime だけ｡

```
statusline ──毎描画── $STATE/context/<sid>.json {used_percentage}
                                    │
daemon (毎 tick) ───────────────────┘
  │
  ├─ 1 段目: 使用率 >= 閾値              → "/agents-daemon:compact-prep" を投入､送信時刻を記録
  ├─ 2 段目: compact-state/<sid>.md の
  │          mtime > 送信時刻            → "/compact <指示文>" を投入
  └─ 3 段目: compacted/<sid> が置かれた  → 再開を促すメッセージを送る
             (PostCompact hook が書く)     → 復旧 hook が state file を注入し marker を消す
```

3 段目は 1・2 段目を経由しない compact でも動く｡marker を書くのは PostCompact hook で
あって daemon ではないため､ユーザーが手で打った `/compact` でも Claude Code 本体の
autocompact でも同じように再開する (PostCompact は `compact_reason` を
`manual` / `auto` で渡し､matcher を書かなければ両方で起動する)｡

## なぜ画面を読まないか

compact 後の画面には圧縮の境界行が残らない｡実機で確認した｡probe した pane を実際に
compact させた直後に `inspect --pane` を掛けると､下の「compact 直後の停止」の
画面判定は「候補ではありません」を返す｡そのため再開の判定は marker に寄せてある｡

## 「compact-prep が走った」の判定

state file の mtime が､daemon が `compactAutoPrepMessage` (既定 `/agents-daemon:compact-prep`) を
送った時刻より新しいこと｡存在だけを条件にすると､消し忘れた 1 つのファイルで同じ pane を延々と
突くことになる｡3 段目の marker も同じ考えで､自分が送った再開より後に置かれた marker しか見ない｡

`compact-state/<sid>.md` の `<sid>` は compact-prep skill が
`CLAUDE_CODE_SESSION_ID` から取る｡この値は herdr の `agent_session.value`
(daemon が pane から引く session_id) と PostCompact hook の stdin の `session_id` に
一致する｡3 つが一致することは実機で確認した｡

## 送信の条件

| 条件 | なぜ要るか |
| ---- | ---- |
| `agent_status` が `idle` / `done` | working な pane へ送るとキューに入り､進行中の作業の直後に実行される |
| 入力欄が空 | 残った文字に送信文字列が連結される ([pane-io.md](pane-io.md)) |
| 使用率の観測が `stateMaxAgeSeconds` 以内 (1 段目) | statusline の描画が止まっている pane は､そのセッションが生きていない |
| 直近の投入から `compactAutoCooldownMinutes` (1 段目) | state file が出ないまま往復しない |
| marker が `compactResumeDelaySeconds` (既定 60 秒) より古い (3 段目) | 圧縮直後に人が自分で続きを打つ余地を残す |

`compactAutoMessage` の既定値が `/compact` 単体でないのは補完メニューのため｡`/compact` は
`/compact-prep` と前方一致でメニューが開き､Enter が候補を横取りしうる｡末尾に指示文
(スペース + 本文) を付けるとコマンド名の補完ではなくなり､メニューが開かない｡実機で確認した
(確認したのは skill が user scope の `/compact-prep` だったとき｡plugin の skill でも候補に出うるので､
指示文は外さない)｡

`PaneState` は in-memory なので､段の合間に daemon が再起動すると送信時刻が失われ､
1 段目からやり直しになる｡害は compact-prep が 1 回余計に走るだけ｡

## compact 直後の停止

上限とは無関係に､compact の直後に作業が終わっていないのにセッションが入力待ちで
止まることがある｡上限の文言は一切出ないので､二重シグナルの検知には一切かからない｡

上の marker 経路が本筋で､こちらはその後ろに残してある二段目の受け皿｡PostCompact hook が
起動しなかった pane (hook の timeout､stdin の `session_id` が空) では marker が置かれず､
3 段目が動かないため｡

上限検知の安全策は「二重シグナル + 使用率ゲート」だが､この事象に相当する裏取りは無い｡
誤って発火すると､ユーザーが意図して置いた idle な pane へプロンプトを打ち込むことになる｡
そのため次を**全て**満たしたときだけ送る｡

| 条件 | なぜ要るか |
| ---- | ---- |
| `compactStallEnabled` が true | 全体スイッチ |
| 画面が limit にも transient にも該当しない | 上限の経路と競合させない |
| 圧縮の境界行がある | 「compact 直後」の唯一の手がかり |
| **境界より後ろに `⏺` の応答行が無い** | 「圧縮しただけで何も進んでいない」と「圧縮してから続きを流した」を分ける |
| 境界より後ろに空の入力行がある | ユーザーが書きかけのテキストを持つ pane へ送らない |
| `agent_status` が `idle` / `done` | 作業中・承認待ちの pane に割り込まない |
| その画面が `compactStallQuietSeconds` (既定 120 秒) 変わらない | 圧縮後に考え始めた Claude へ割り込まない |
| 直近の送信から `compactStallCooldownMinutes` (既定 10 分) 経過 | 送っても画面が変わらないときに突き続けない |
| 1 回の停止 (送っても画面が変わらず続く状態) への送信が `compactStallMaxNudges` (既定 3 回) 未満 | cooldown を跨いでも進まない run は詰まっている｡送らずログに `compact-nudge-capped` を残し､pane は入力待ちのまま人の判断に委ねる｡応答が流れて停止が解けたら数え直す |

画面が変わっていないかを見る「署名」には､圧縮の境界行から空の入力行までしか含めない｡
ステータスラインにはコストやトークン数のような更新されうる表示が並ぶので､そこまで
含めると署名が永久に安定せず､検知が黙って死ぬ｡

境界行の判定には**括弧付きのヒントを必須**にしてある (実際の UI 行は必ず
`(ctrl+o …)` のような案内を伴う)｡この語に触れただけの散文 — この文書を画面に
出した pane を含む — で発火すると､無関係な pane へプロンプトを打ち込む事故になる｡

**この画面判定が実機の compact 直後に発火しないことを確認している｡** 実際に
compact させた pane に `inspect --pane` を掛けると「候補ではありません」を返した｡

確認できている原因は 1 つ｡空の入力行の判定が U+00A0 に当たっていなかった
(`sp` の宣言を参照)｡それを直した後､同じ状況を再現して確かめてはいない｡境界行の
文言自体も Claude Code のバイナリの描画コードから読んだもので､現物と突き合わせていない｡

当てにするのは marker 経路 (上の 3 段目) で､こちらは marker が届かなかったときの
ための残置｡次に compact が起きた pane で `inspect --pane <id>` を叩いて
「候補です」と出るかを見れば､境界行が生きているかが分かる｡

`agent_status` を条件に使うのは上限検知の方針
([rate-limit.md](rate-limit.md)) と逆に見えるが､向きが違う｡上限検知で
使えないのは「働いていない pane が `working` と報告される」ことがあるためで､ここは
「働いていないこと」を要求する側なので､誤っても送らない方へ倒れる｡
