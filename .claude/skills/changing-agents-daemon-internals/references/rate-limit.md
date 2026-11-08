# 利用上限の検知と待機

画面の読み取り元と送信の作法は [pane-io.md](pane-io.md)、毎 tick の判定表は
[architecture.md](architecture.md) にある。利用者向けの既知の制約と設定キーは plugin の
`agents-daemon:agents-daemon` skill の `references/operations.md`。

## 上限かどうかは transcript で決める

<!-- cc-premise: 2.1.319 limit-screen API error の entry は type=assistant、isApiErrorMessage=true、error (rate_limit など)、quotaLimits (status、rateLimitType、resetsAt epoch 秒) -->

セッションが上限で止まっているかは、そのセッションの transcript
(`~/.claude/projects/<project>/<session_id>.jsonl`) の**末尾の会話の entry** で決める (`transcript.Store.Tail` →
`detect.ClassifyTurn`)。画面の文字列では決めない (#257)。

pane の画面には Claude Code が描いた通知と、tool の出力に引用された任意の文章が同じ平面に並ぶ。issue のタイトル、
ログの引用、この文書のような設計メモが上限の文言をそのまま含む。字面では区別できず、二重シグナル (上限の語と解除時刻の
並び) や使用率のゲートを足しても、切り替え前の limit-events に残っていた 15 件のうち本物の上限は 0 件だった。
transcript は Claude Code が自分の会話として書くもので、tool の出力は別の entry になる。上限に弾かれた turn は
`type=assistant` の entry に `error=rate_limit`、`isApiErrorMessage=true`、`quotaLimits` (`status`、`rateLimitType`、
`resetsAt` の epoch 秒) が付いて終わる。解除時刻が epoch で直接手に入るので、画面の `resets 12:30pm (Asia/Tokyo)` を
am/pm やタイムゾーンごとパースする処理も要らない。

読むのは末尾の 1 entry だけ。`type` が `user` / `assistant` で、`isMeta` でも `isSidechain` でもないものが会話の
entry で、それ以外 (summary、system、subagent の記録) は飛ばす。transcript は起動時の cwd ごとの project ディレクトリに
書かれ、EnterWorktree で cwd が変わると別の project ディレクトリへ移る (実測) ので、
毎回 `projects/*/<sid>.jsonl` を引き、複数あれば mtime が新しいものを採る。末尾から 64 KB ずつ、8 MB まで遡る。
形は Claude Code の内部仕様で版で変わりうるので、読めなくなったら `Tail` が理由を返し、daemon は判定の対象外として
理由を limit-events と status.json に残す (`transcript-unavailable`)。

### session_id の引き方

transcript を引くには pane のセッションの ID が要る。statusline の記録 (`context/<sid>.json` の `multiplexer`、
[statusline-state.md](statusline-state.md)) で引き、無ければ rex の前景の pid で Claude Code の登録簿
(`~/.claude/sessions/<pid>.json`、`claudesessions`) から補う。登録簿の pid は claude の process の pid で、rex の
foreground の pid と一致する (実測)。claude を wrapper の shell から起動すると pid が合わず、statusline の配線が要る。
どちらも引けない pane は判定の対象外 (`no-session`。`doctor` の pane 一覧に ⚠、status.json に `detect_reason`)。

登録簿で補った session_id は status.json、セッション台帳、辺の結びつけ、依存図にも流れる。statusline を配線していない
セッションも依存図に現れる。

### 分類 (`detect.Kind`)

| 末尾の entry | Kind | daemon |
| ---- | ---- | ---- |
| `error=rate_limit` で `quotaLimits.resetsAt` があり、`rateLimitType` が `seven_day` 系でない | `limit` | 解除まで待つ |
| `error=rate_limit` で `resetsAt` があり、`rateLimitType` が `seven_day` 系 | `weekly-limit` | 解除が 24 時間以内なら待つ (下) |
| `error=rate_limit` で `quotaLimits` が無く、本文が spend limit / usage credit / credit balance too low / out of credits / Fable の上限 | `spend-limit` | 待たない (hard cap) |
| `error=rate_limit` で `quotaLimits` が無く、本文が `API Error` で始まる (429 の Request rejected) | `transient` | 短い待機で再試行 |
| `error=rate_limit` で `quotaLimits` が無く、上のどれでもない (2.1.233 以前の上限の文) | `none` | 追わない |
| `error` が `overloaded` / `server_error` | `transient` | 短い待機で再試行 |
| `error` が `invalid_request` / `billing_error` / `unknown`、error の無い entry、`type=user` | `none` | 何もしない |

`quotaLimits` の無い上限の文を追わないのは、解除時刻が無いので待つ理由を決められないため。2.1.234 以降の上限の entry は
必ず `resetsAt` を持つ (手元の 159 件)。見逃しは StopFailure の記録との食い違いのログで気づく (下の「hook は補助」)。
知らない `rateLimitType` でも `resetsAt` があれば `limit` として待つ。

### hook は補助

StopFailure hook (`hooks/ingest-turn-failure.sh` → `turnfailure`) は API error で turn が終わったときに走り、
`error` と `last_assistant_message` を `turn-failures/<sid>.json` に残す。次の turn の Stop / UserPromptSubmit で
消える。daemon は判定には使わず、上限の出来事に `turn_failure` として添える。StopFailure が `rate_limit` なのに
transcript が上限と読めなければ、transcript の形が変わった疑いとして daemon.log に 1 行残す (理由が変わったときだけ)。

hook を主にしないのは、hook が走らないセッション (plugin を有効にしていない、`-p` の非対話) があることと、
hook は「最後の turn がどう終わったか」しか知らず解除時刻を持たないため。Notification hook の
`quota_auto_resume_*` を Enter の根拠に足す案は #529。

## 待つかどうか (`monitor.Decide`)

監視中の pane の末尾の entry をどう扱うかは `monitor.Decide` の 1 箇所で決め、tick と `doctor` と `inspect` が同じ関数を
呼ぶ。診断の説明と実際の挙動がずれないようにするためで、ここ以外に「待つかどうか」の判断を書かない。

| 条件 (上から順に見る) | Plan | outcome |
| ---- | ---- | ---- |
| `spend-limit` | hard-cap | `hard-cap` (待たない) |
| `none` | none | `monitoring` |
| `weekly-limit` で解除が 24 時間より先 | weekly-far | `weekly-cap` (待たない。監視のまま毎 tick 見直す) |
| 解除 (解除時刻の無い `transient` は entry) から 24 時間より経った、または時刻が読めない | stale-turn | `stale-turn` (待たない) |
| それ以外の `limit` / `weekly-limit` / `transient` | wait | `waiting` |

`transient` を扱うか (`handleTransient`) は Decide の外で先に外す。

### 週の上限は解除が 24 時間以内なら待つ (#338)

seven_day 系の上限は解除が数日先になりうる。本体の auto-continue が降りる線 (解除が 24 時間より先) と同じく、
24 時間以内なら 5 時間ウィンドウと同じ経路で待ち、それより先なら待たず記録だけ残す。監視のまま毎 tick 見直すので、
解除が 24 時間以内に入った tick で待機に入る。待っている間に別の依頼が週の上限に当たり、解除が 24 時間より先へ移れば
監視へ戻る。

Ultra (5 時間ウィンドウの無いプラン) では statusline の `rate_limits` に `seven_day` だけが来るが、daemon は
statusline の state を読まないので判定には関わらない。残るのは `doctor` の rate-limits の表示 (#338 の残り)。

### 解除から 24 時間より経った entry には送らない (stale-turn)

daemon の起動前や `claude --resume` の前に止まったまま誰も戻っていない pane に、再開の文面を送らない。基準は
**解除時刻 (`resetsAt`) から 24 時間**で、entry の時刻ではない。週の上限では踏んでから解除まで数日あり、entry の古さで
止めると解除が近づいた頃には必ず古くなっていて、永遠に待機に入れない。daemon の再起動で待機の状態を失った pane も、
解除から 24 時間以内なら監視からやり直して待機に入れる。解除時刻の無い一時エラーは entry の時刻で見る。時刻の読めない
entry は送らない側に倒す。

見るのは待機に入る時点だけで、待機明けには見ない (待っている間に 24 時間を越える経路を止めないため)。

## 起床時刻 (`monitor.ComputeLimitWake`)

情報源は transcript の entry の `quotaLimits.resetsAt` だけ。未来ならその時刻 + `marginSeconds`、過去なら
今 + `marginSeconds` (解除時刻を過ぎても Claude Code は上限の画面のままキー入力を待つので、待つ理由が無い)。
`resetsAt` の無い entry は `ClassifyTurn` が `limit` にしないのでここには来ない。

statusline の state (`rate-limits/`) を予備に使う経路と `fallbackWaitHours` は #528 で消した。2.1.234 以降の上限の
entry は必ず `resetsAt` を持ち、無い entry を追っても待つ理由が決められないため。`rate-limits/` を読むのは
`doctor` (statusline の配線の確認) だけになった。

待っている間に別の uuid の上限の entry が出て (peer の SendMessage などで新しい依頼が弾かれた)、解除時刻が未来なら
送らず待ち直す。試行回数は持ち越す。

## record が working の pane には触らない

rex の `agent_status` は Claude Code 自身が OSC 7501 で申告する state ([program-status.md](program-status.md))。
`working` の pane には transcript が何と言っていても触らない。turn の途中、retry watchdog の待機中、
lower-priority mode の作業中はどれも `working` で申告される。transcript が上限なのに `working` なら、書き込みの遅れか
「上限で止まった pane は working でなくなる」前提が外れたかの実測として `suppressed-working` を limit-events に残す。
待機中に `working` になったら、誰か (利用者、peer、本体の auto-continue) が再開したので監視へ戻る
(`recovered-by-user`)。

待機明けの送信は、Claude Code が `blocked` で kind が `permission` / `question` / `auth` と申告している
(利用者が答える prompt が開いている) 間は見送って待機を延長する (`blocked-by-prompt`)。esc は prompt を断り、Enter は
承認するため。

## 待機明けに画面を読んでキーを選ぶ (`detect.ClassifyScreen`)

画面を読むのは待機明けで、transcript がまだ上限 (か一時エラー) のときだけ。読むのは末尾 `readLines` 行で、通知欄の判定は
更に末尾 `detectionTailLines` 行だけを見る。読んだ画面と判定 (`Hint`) は送った tick の limit-events に残る。

| 通知欄 | Hint | 送るもの |
| ---- | ---- | ---- |
| request の中でリトライ中のスピナー | `native-retrying` | 手を引く (監視へ戻る) |
| auto-continue の予約 | `native-armed` | 手を引く (`deferToNativeAutoContinue` が false なら通常の再開) |
| 解除済みで Enter 待ち | `resume-ready` | **Enter だけ** |
| lower-priority mode で作業中 | `lower-priority` | 手を引く (#251) |
| どれでもない | `none` | esc → `retryMessage` → enter |

上限かどうかの判定には画面を使わないので、引用された文で待機に入ることは無い。通知欄の判定が引用で誤ると送るキーが変わる
だけで、それも `Hint` の表の中でしか動かない。固定した本物の文言は `detect` のテスト (`native_continue_test.go`、
`native_retry_test.go`、`footer_test.go`、`lower_priority_test.go`) にある。この文書の文言を伏せてあるのは、画面に出した
pane が待機明けに「Enter 待ち」と読まれるのを避けるため (判定そのものは transcript なので待機には入らない)。

### Claude Code 自身の auto-continue との関係

<!-- cc-premise: 2.1.299 limit-screen autoContinueAtUsageLimit のバナー・取り消し・Enter 待ちの文言と Escape の意味 -->

`autoContinueAtUsageLimit` (既定 on) により、上限が解除されると Claude Code 自身が中断された turn を流し直す。
**このツールと取り合いになる。** ネイティブが再開を予約している間、画面には次のようなバナーが出る (時刻は伏せてある):

```
Usage limit reached · continuing automatically at HH:MMpm · esc or type to cancel
```

末尾に注目。**Escape はネイティブ側の取り消しキー**でもある。このツールの再開送信は `/rate-limit-options` メニューを
閉じるために Escape から始まるので、解除時刻に起きて送ると、ネイティブがちょうど流そうとしていた継続をその Escape が
潰す。しかもネイティブは中断された turn をそのまま続けるのに対し、こちらは `retryMessage` で新しい依頼を出す。
文脈の乏しい方が勝つことになる。そのため **バナーが出ている間は手を出さない** (`deferToNativeAutoContinue`、既定 true)。

判断は画面のバナーだけで行い、設定ファイル (`autoContinueAtUsageLimit`) は読まない。ネイティブが実際に予約するかは、
その設定に加えて課金形態 (usage-based では動かない)・overage の使用有無・解除が 24 時間以内かで決まる。それらが
解決された結果が出るのは画面だけである。

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
| `Automatic continue cancelled …` | セッションの移動・再起動等で取り消された | 通常の再開を送る |
| `Automatic continue stopped …` | 解除が 24 時間以上先 / 再到達が続いた | 通常の再開を送る |
| `Automatic continue was turned off …` | 設定で無効化されている | 通常の再開を送る |

`Enter だけ`にしているのは、テキストを打つと中断された turn の続きではなく新しい依頼になってしまうため。
ネイティブが解除まで待ってくれた文脈を捨てずに済む。この Enter は通常の上限と同じ状態機械に乗せてある
(待機 → 送信 → `maxRetries` で打ち切り)。専用の経路にすると試行回数の数え上げから外れ、Enter が効かない pane を
30 秒おきに永久に叩き続けることになる。

**取り消しの通知は予約中のバナーより優先する。** バナーは画面下部で描き直される要素なので、取り消されても直前の
フレームがスクロールバックに残る。古いバナーを見て手を引き続けると、ネイティブも動かずこのツールも動かない状態で
固まる。そのため走査範囲に `Automatic continue cancelled / stopped / was turned off / did not run` のいずれかがあれば、
同じ画面にバナーがあっても予約中とは見なさない。

### Claude Code が request の中でリトライしている間は手を出さない

<!-- cc-premise: 2.1.316 limit-screen retry のスピナー行 (バイナリのテンプレート、実機未確認) -->

unattended retry mode (`CLAUDE_CODE_RETRY_WATCHDOG`、env の opt-in) は、usage limit に当たった request を終わらせず、
解除まで request の中で待つ。待っている間、スピナー行は `⚠ Session limit reached · Retrying in 3h 12m (5pm)` の後ろに
試行回数を添えた形になる。Escape はこの待機ごと request を取り消す。この間 record は `working` なので監視中は
触らないが、前の request の上限の entry で待機に入った後にこの画面になることはある。そのため、スピナーの
`Retrying in` の後ろ (同じ行か、折り返した次の行) に試行回数があれば、ほかの判定より先に手を引く。5xx や overloaded の
リトライも同じ行になり、どれも本体が待っているので区別しない。`deferToNativeAutoContinue` には従わない。watchdog は
auto-continue とは別の opt-in で、この設定を切っても watchdog の待機を潰してよい理由にならない。

### lower-priority mode で作業中は手を引く

<!-- cc-premise: 2.1.310 limit-screen lower-priority mode の作業中と終わりの通知 -->

session limit に当たった後、`/low-priority` で上限を越えて低優先度の作業を続けられる (flag 付き)。pane は作業中なので
record は `working` になるはずだが、実測では `idle` の 1 件がある ([program-status.md](program-status.md))。待機明けに
作業中の通知 (`Continuing now at lower priority until your limit resets at …`) があれば送らず監視へ戻る (#251)。
終わった通知 (`Lower-priority mode ended`、`… stopped`、`… is off` など) が同じ画面にあれば、通常の上限に戻す。
作業中の通知は scrollback に残るので、取り消しの通知と同じく終わった側を優先する。

## 待っても解除されない上限では待機に入らない

<!-- cc-premise: 2.1.316 limit-screen Fable の上限の行とダイアログの文言 (バイナリのテンプレート、実機未確認) -->

月次 spend limit と usage credit の上限は、5 時間ウィンドウの解除では解けない。待機して送信 → 再び弾かれる、を
`maxRetries` まで繰り返すだけになるので、`hard-cap` として記録し待機に入らない。上流も同じ扱いで、retry watchdog は
organization spend-limit と out-of-credits で即座に失敗する。

Fable のモデル単位の上限 (`/usage` の `Current week (Fable)` の行) も同じ扱い。entry は `error=rate_limit` で
`quotaLimits` が無く、本文が `You've reached your Fable 5 limit. Run /usage-credits to continue …` の形
(2.1.251 の実物)。続けて出るダイアログは別モデルか使用クレジットで続けるかを聞き、Enter が選ぶ既定の選択肢は
クレジットで続ける側にある。モデルを切り替えるのは本体と利用者の判断なので、daemon は待たずに手を引く。
Opus / Sonnet 単位の週次上限は `rateLimitType` が `seven_day_opus` / `seven_day_sonnet` の `weekly-limit` で、
全モデル共通の週次上限と同じく解除が 24 時間以内なら待つ。

## 一時エラー

`error` が `overloaded` / `server_error` の entry と、`quotaLimits` の無い `rate_limit` で本文が `API Error` で
始まるもの (アカウント全体の rate limit、429 の Request rejected) は `transient`。`transientWaitSeconds` から始まる
指数バックオフ (`transientMaxWaitSeconds` で頭打ち) で待ち、待機明けに画面を読んで送る。`maxRetries` の対象外で、
本体がリトライする間は `working` になるので、送信 → working → 監視へ戻る → 再検知の輪に回数の上限は無い。

Claude Code 自身がリトライしている間は record が `working` なので触らない。諦めて `error` の entry を書いてから
引き取る。`handleTransient` を false にすると `transient` には何もしない。待っている間に上限から一時エラーへ
変わったときも監視へ戻る。

## 上限中に開いた新規セッション

上限中に開いた新規セッションで `retryMessage` が意味を持つのは、プロンプトを打って弾かれた場合だけ
(弾かれたプロンプトは会話に残っているので、続きを頼めば進む)。何も打っていないセッションの transcript には
上限の entry が無いので、daemon は何もしない。
