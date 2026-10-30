# 利用上限の検知と待機

画面の読み取り元と送信の作法は [pane-io.md](pane-io.md)、毎 tick の判定表は
[architecture.md](architecture.md) にある。

## Claude Code 自身の auto-continue との関係

<!-- cc-premise: 2.1.299 limit-screen autoContinueAtUsageLimit のバナー・取り消し・Enter 待ちの文言と Escape の意味 -->

`autoContinueAtUsageLimit` (既定 on) により、上限が解除されると Claude Code
自身が中断された turn を流し直す。**このツールと取り合いになる。**

ネイティブが再開を予約している間、画面には次のようなバナーが出る (時刻は伏せてある):

```
Usage limit reached · continuing automatically at HH:MMpm · esc or type to cancel
```

末尾に注目。**Escape はネイティブ側の取り消しキー**でもある。このツールの再開送信は
`/rate-limit-options` メニューを閉じるために Escape から始まるので、解除時刻に起きて
送ると、ネイティブがちょうど流そうとしていた継続をその Escape が潰す。しかもネイティブは
中断された turn をそのまま続けるのに対し、こちらは `retryMessage` で新しい依頼を出す。
文脈の乏しい方が勝つことになる。

そのため **バナーが出ている間は手を出さない** (`deferToNativeAutoContinue`、既定 true)。
待機中に起きてバナーを見つけたときも、送らずに監視状態へ戻す。

判断は画面のバナーだけで行い、設定ファイル (`autoContinueAtUsageLimit`) は読まない。
ネイティブが実際に予約するかは、その設定に加えて課金形態 (usage-based では動かない)・
overage の使用有無・解除が 24 時間以内かで決まる。それらが解決された結果が出るのは
画面だけである。

<!-- cc-premise: 2.1.310 limit-screen 通知欄は status と next を別の行に描く -->

表示は 2 か所に出る。状態が変わった瞬間に transcript へ足される上の 1 行と、待っている間ずっと prompt のそばの
通知欄に出る 2 行 (`Usage limit reached · limit resets …` の下に、字下げした `Continuing automatically …`。
解除済みなら `Your usage limit has reset` の下に `Press enter to continue`) だ。transcript の 1 行が走査範囲の外へ出ると、
残るのは通知欄だけになる。そのため通知欄の status の行と、その 2 行以内の next の行の組も同じ状態として扱う。
引用された文で発火しないよう、どちらの行も行頭から一致させる。

ネイティブが降りたときは別の文言になり、そこからはこのツールの担当に戻る:

| 画面 | 意味 | このツール |
| ---- | ---- | ---- |
| `… continuing automatically …` / `… continuing shortly …` | 再開を予約している | 手を出さない |
| `Usage limit has reset · press enter to …` | 解除済みだが継続の窓を過ぎた | **Enter だけ**を送る |
| `Automatic continue cancelled …` | セッションの移動・再起動等で取り消された | 通常の上限として扱う |
| `Automatic continue stopped …` | 解除が 24 時間以上先 / 再到達が続いた | 通常の上限として扱う |
| `Automatic continue was turned off …` | 設定で無効化されている | 通常の上限として扱う |

`Enter だけ`にしているのは、テキストを打つと中断された turn の続きではなく新しい依頼に
なってしまうため。ネイティブが解除まで待ってくれた文脈を捨てずに済む。

この Enter は通常の上限と同じ状態機械に乗せてある (待機 → 送信 → `maxRetries` で
打ち切り)。専用の経路にすると試行回数の数え上げから外れ、Enter が効かない pane を
30 秒おきに永久に叩き続けることになる。待つ理由は無いので起床時刻だけ「今」にする。

**取り消しの通知は予約中のバナーより優先する。** バナーは画面下部で描き直される要素なので、
取り消されても直前のフレームがスクロールバックに残る。古いバナーを見て手を引き続けると、
ネイティブも動かずこのツールも動かない状態で固まる。そのため走査範囲に
`Automatic continue cancelled / stopped / was turned off / did not run` のいずれかが
あれば、同じ画面にバナーがあっても予約中とは見なさない。

上の表で 2 行目の文言を `…` で伏せてあるのは、解除時刻を伏せてあるのと同じ理由。
そのまま書くと、この文書を画面に出した pane が「Enter 待ち」と判定され、
**実際に Enter を打ち込まれる**。固定した本物の文言は
`internal/runtime/detect/native_continue_test.go` にある。

`deferToNativeAutoContinue` を false にすると、バナーが出ていても通常の上限として
自前で待機する。ネイティブを `/config` で切っている場合はバナー自体が出ないので、
この設定を触る必要は無い。

## 待っても解除されない上限では待機に入らない

月次 spend limit を使い切ったときの上限メッセージにも、session / weekly の
リセット時刻が併記される。二重シグナルの要件を満たしてしまうが、
待っているのは 5 時間ウィンドウの解除であって spend limit の解除ではない。待機して
送信 → 再び弾かれる、を `maxRetries` まで繰り返すだけになる。

そのため limit が成立した画面に spend limit / usage credit limit / credit balance too low /
out of credits のいずれかがあれば、待機に入らず `hard-cap` として記録する。判定する
文言は `internal/runtime/detect` が持つ。上流も同じ扱いで、retry watchdog は organization
spend-limit と out-of-credits で即座に失敗する。

## lower-priority mode の画面は印を付けて残す

<!-- cc-premise: 2.1.310 limit-screen lower-priority mode の作業中と終わりの通知 -->

session limit に当たった後、`/low-priority` で上限を越えて低優先度の作業を続けられる (flag 付き)。その間の通知
(`Continuing now at lower priority until your limit resets at …`) は上限の行と並び、二重シグナルを満たすので、
pane は作業中でも limit と判定される (#251)。実害をまだ見ていないので判定は変えず、気付けるようにだけしてある。
その画面を上限の出来事として記録するとき、`limit-events/` の行に `lower_priority: true` を付け、daemon.log に
1 行残す。

終わった通知 (`Lower-priority mode ended`、`… stopped`、`… is off` など) が同じ画面にあれば印を付けない。
作業中の通知は scrollback に残るので、取り消しの通知と同じく終わった側を優先する。上限の画面に並ぶ案内
(`/low-priority to continue now …`) も、まだ低優先度で動いていないので印を付けない。文言は `internal/runtime/detect` が持つ。

## `agent_status` は判定に使わない

rex の `agent_status` は Claude Code 自身が OSC 7501 で申告する state ([program-status.md](program-status.md))。
上限で止まった pane は turn の途中で止まるので、`working` のまま申告され続ける。
`working` を対象外にすると、上限で止まった pane の画面を一度も読まないまま待ち続ける。

**上限で止まった pane は、`agent_status` からは「働いている pane」と区別できない。**
そのため `agent_status` は status 表示のためだけに残し、判定は画面テキストの分類だけで行う。
稼働中の pane も毎 tick 読むことになるが、読み取りは副作用が無い。

例外は待機明けの送信の抑止だけ。Claude Code が `blocked` で kind が `permission` / `question` / `auth` と
申告している (利用者が答える prompt が開いている) 間は、画面が上限に見えても送らず待機を延長する
(`blocked-by-prompt`)。esc は prompt を断り、Enter は承認するため。限度の画面がこの kind で来ることは無いので、
再開を止める側には倒れない ([program-status.md](program-status.md))。

## limit の判定は 2 つの手がかりを要求する

画面に limit を示す文言があるだけでは発火しない。近く (前後 6 行以内) に reset 時刻を示す
文言が必要。実機で観測した本物の表示は 1 行に両方が入っている:

```
You've hit your session limit · resets HH:MMpm (Asia/Tokyo)
```

時刻を `HH:MM` に伏せてあるのは、実際の数字を書くとこの文書自身が二重シグナルを
満たして limit と判定されるため。Claude がこのファイルを画面に表示した pane を
誤検知する。固定した本物の文言は `internal/runtime/detect/real_sample_test.go` にある。

`85% of your 5-hour limit` のような**使用率の警告行は明示的に除外する**。上限に当たる前に
必ず出る行で、これを拾うと余裕があるうちに待機へ入ってしまう。

## ゲート

**その pane のセッションの** state が新しい (既定 15 分以内) とき、5 時間ウィンドウの
使用率が `usedPercentageThreshold` (既定 90%) 未満なら、画面が limit に見えても発火し
ない。スクロールバックに残った過去の文言や、この文書のような文字列自体での誤爆を弾く。

`agent_status` を判定に使わなくなったぶん、誤検知に対する実質的な防御はこのゲートが担う。
そのため「どのセッションの state を見るか」がそのまま安全性に効く。

state が無い / 古い / `five_hour` が null のときは判断材料が無いため、ゲートは適用せず
画面だけで判定する。

**ゲートが効くのは待機に入るときだけ**で、待機明けには掛けない。解除時刻を過ぎると
5 時間ウィンドウは切り替わって使用率は下がるが、Claude Code は上限の画面のままキー入力を
待つ。ここで fresh な低使用率を理由に見送ると「ユーザー自身が復帰した」と取り違えて監視へ
戻り、state が古くなってから改めて検知して、次のウィンドウの `resets_at` (5 時間先) まで
寝てしまう。待機明けに画面がまだ limit なら、使用率によらず送る。

## transient は Claude Code 自身のエラー行だけを見る

一時エラーの語句は、Claude Code がリトライを諦めて描くエラー行 (接頭辞 `API Error`) にあるときだけ拾う。
画面には tool result の任意の文章も流れ、issue のタイトルやログの引用に同じ語句は普通に現れる。
2026-10-10 にこれで作業中の pane が待機に入った。transient には limit のゲートが無いので、
この接頭辞が唯一の裏取りになる。

Claude Code 自身がリトライしている間のカウントダウン行には接頭辞が付かないので拾わない。
途中で Escape を送ると回復中の turn を潰す。諦めてから引き取る。

## 待機時刻の決め方

情報源は 3 つある。その pane のセッションの state (own)、画面に出る絶対時刻、
アカウント全体で最も新しいウィンドウの state (account)。優先順位は次のとおり。

1. own の `resets_at` が未来 → その時刻 + `marginSeconds`
2. 画面の絶対時刻 (`resets HH:MMam (Asia/Tokyo)` の形) が読める → 未来ならそれ +
   `marginSeconds`、過去なら `marginSeconds` 後
3. account の `resets_at` が未来 → その時刻 + `marginSeconds`
4. own / account の `resets_at` が過去 → `marginSeconds` 後 (5 時間ウィンドウは既に切り替わっている)
5. 画面から `try again in NN minutes` のような相対表現が取れる → それ + `marginSeconds`
6. どれも無い → `fallbackWaitHours` + `marginSeconds`

実機で観測した表示は絶対時刻なので 5 は効かない。1 が本命で、6 は保険。

### own state を持たない pane (2 と 3)

上限中に開いた新規セッションには state ファイルがまだ無い。statusline が `rate_limits` を
受け取るのは API レスポンスの後で、上限に弾かれた turn (429) では書かれない。own だけを
見ると 6 に落ちて 5 時間寝る。2026-08-28 07:35 に実際にこの形の pane を検知し、解除
時刻が 35 分先だったのに起床時刻が 5 時間先と算出された。

`resets_at` はアカウント全体で共通の値である。`rate-limits/` 配下の state ファイルを
observed_at 順に並べると、同じウィンドウ内に観測された全セッションが同じ `resets_at` を
持つ (例: あるウィンドウを 8 セッションが共有)。そのため他セッションの state から現在の
ウィンドウの解除時刻を引ける (3)。`ratelimit.Store.LatestWindow` が `resets_at` 最大のものを
選ぶ。now+6 時間より先の値はゴミとして無視する (5 時間ウィンドウの解除が 5 時間より
先になることは無く、1 ファイルの不良で全 pane の起床時刻を汚さないため)。

画面の時刻 (2) はその pane が止まったウィンドウを直接示す。実機の表示は
`HH:MMam/pm (tz名)` で形が固定されており、tz 名は `time.LoadLocation` で解決できる
(無ければローカル)。日付が無いので、昨日 / 今日 / 明日の候補のうち now+6 時間を超えない
最も遅いものを採る。am/pm の無い数字は読まない。走査するのは limit 行の近傍にある
reset 表現だけで、使用率の警告行は対象外。

2 を 3 より上に置くのは、解除後に上限の画面のまま止まっている pane のため (ネイティブの
auto-continue が取り消された等)。他セッションが次のウィンドウで動き出すと account は
「次のウィンドウの終わり」を指すが、画面の時刻は過去のままなので今すぐ突ける。逆に
own が過去で画面が未来のときは、own が前ウィンドウの残骸で pane は現在のウィンドウで
弾かれている。2026-08-28 07:34 に daemon が送った再開はこれで、own の `resets_at`
(03:10) が過ぎていることを「解除済み」と読んで、まだ上限中の pane へ送っていた。

4 が要るのは、解除時刻を過ぎても画面が上限表示のまま止まることがあるため。Claude Code は
そこでキー入力を待つ。ここで 6 へ落ちると、待つ理由が無いのに `fallbackWaitHours` ぶん
寝てしまう。2026-08-19 の pane は実際にこの状態で、解除時刻 05:00 を 4 時間過ぎた時点でも
起床時刻が 5 時間先と算出されていた。

見ているのは 5 時間ウィンドウだけ。週次上限で止まった pane はこの計算では正しく扱えず、
送信 → 再度上限 → 再試行を `maxRetries` まで繰り返してクールダウンに入る。

**1 と 4 には鮮度 (`stateMaxAgeSeconds`) を要求しない。** `resets_at` は絶対時刻で、使用率の
ように時間で腐る値ではない。むしろ上限で止まったセッションには新しい数値が届かない
(statusline は受け取った window の `resets_at` で 1 回走るが、その window は落とされて来ない) ため、
state は「一番必要なタイミングで必ず古い」。2026-08-19 の事例では最後の書き込みが
02:02、解除時刻が 05:00 で、鮮度を要求すると 02:17 以降は正しい 05:00 を捨てて
`fallbackWaitHours` へ後退していた。採用条件は「未来かどうか」だけにしてある。

ゲート (使用率による抑制) は own state だけで判定し、account は使わない。account の
fresh な低使用率で抑制すると、解除後に止まったままの pane が他セッションの活動中ずっと
抑制される。account が効くのは起床時刻の算出だけ。

上限中に開いた新規セッションで `retryMessage` が意味を持つのは、プロンプトを打って
弾かれた場合だけ (弾かれたプロンプトは会話に残っているので、続きを頼めば進む)。
何も打っていないセッションへ送っても「続きは無い」と返るだけで、害は無いが始まりもしない。
