# agents-daemon のコマンド・設定・制約

## コマンド

バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` にある｡PATH には入れていないので､
手で叩くときは絶対パスで呼ぶか､そのディレクトリを PATH に足す｡

```sh
agents-daemon daemon --ensure [--dry-run]      # 居なければ detach 起動 (hook 用)
agents-daemon daemon --foreground [--dry-run]  # このプロセスで回す (デバッグ用)
agents-daemon status                           # 稼働状況､監視中の pane､ログの末尾
agents-daemon logs -n 80                       # 現行ログ (daemon.log) の末尾
agents-daemon stop                             # SIGTERM を送って停止
```

### `--dry-run`

検知も判定も本番と同じ経路で行い､**pane への送信とラベル更新だけをしない**｡
「本来なら何を送っていたか」がログに残る｡

稼働中のセッションへ誤ってプロンプトを打ち込む事故は取り返しがつかないため､
送信経路そのものを塞げる状態を用意してある｡`--ensure` から detach 起動するときも
このフラグは引き継がれる (引き継がないと `--ensure --dry-run` が実際には送信する
デーモンを生む､という最も危険な取り違えになる)｡

SessionStart hook は `--dry-run` **なし**で配線してある｡検知の挙動を疑ったときや､
設定を変えて様子を見たいときに､手動で `--foreground --dry-run` を回す使い方を想定している｡

### 検証用コマンド

```sh
agents-daemon doctor                    # 前提条件の診断
agents-daemon inspect --pane <id>       # 生きた pane の画面を分類する
agents-daemon inspect --file <path>     # 保存した画面テキストを分類する
agents-daemon inspect --file <path> --session <id>  # ゲート判定に使うセッションを指定する
agents-daemon simulate                  # 使い捨て pane で検知から送信まで通す
agents-daemon simulate --send --keep    # 実際に送信し､pane を残して目で見る
```

hook や statusline から呼ばれる `ingest-statusline` / `ingest-stop` は stdin の JSON を受けて state file を
書くだけで､手で叩くなら `< in.json` で渡す｡どちらも stdout には何も出さず､失敗しても exit 0 で終わる｡

`inspect` は判定結果だけでなく､走査した行・ゲートが開いているか・待機時刻をどの
情報源から得たかを出す｡検知がおかしいと疑ったときに､結論ではなく判断過程を見るためのもの｡

ゲート判定には「どのセッションの state を見るか」が要る｡`--pane` を指定したときは
pane から解決する｡`--file` にはセッションが無いため `--session` で明示する｡
指定が無ければゲート判定を行わない旨を出力して､画面の分類だけを報告する｡

`simulate` は使い捨ての pane を作り､herdr に Claude agent として登録し､limit 風の画面を
書き込んで､デーモンと同じ判定を通す｡既定では送信しない｡自分が作った pane 以外には
決して触らない｡

### 実機での検証順序

誤検知しないことの確認を先に置く｡検知できることの確認はその後｡

1. `task install` (リポジトリの clone で､hook と同じ cache のパスへ建てる)
2. `doctor` — 前提が揃っているか
3. `inspect --pane <自分の pane>` — 通常の画面が `none` と判定されること
4. `inspect --file <limit 画面のサンプル>` — `limit` と判定され､待機時刻が `resets_at` 由来であること
5. `daemon --foreground --dry-run` — 実 pane 相手に流し､誰にも送信せず誤爆もしないこと
6. `simulate` — 検知から送信までを end-to-end で通す

### この環境で実際に確認したこと

2026-08-16､Ghostty + herdr 0.8.0 で通した結果｡

- 開発中のセッション自身が 5 時間上限に到達し､本物の表示を取得した
  (`You've hit your session limit · resets HH:MMpm (Asia/Tokyo)`､時刻は上記の理由で伏せてある)｡
  この文言に対する分類は `internal/detect/real_sample_test.go` に固定してある｡
- 同じ事象での `resets_at` は `1786851000` = 12:30 JST で､画面表示と一致した｡
- SessionStart hook からデーモンが自動起動することを確認｡
- `idle` の Claude セッションを誤検知しない｡
- 画面が limit に見えるが使用率が閾値未満の pane で､ゲートが実際に発火を抑制した｡
- 別ワークスペースの pane も監視対象に入る｡
- `simulate` が検知 → 待機 → 再開送信 (esc / テキスト / enter) まで通した｡

そして本番投入した当日､2 つのセッションが実際に上限へ到達し､**自動再開は失敗した**｡
待機のタイマーは正しく動いた (`resets_at` 17:50 + margin 60 秒 → 17:51 ちょうどに起床) が､
送信が毎回エラーになり再試行上限まで進んだ｡原因は herdr の応答の解釈違いで､
成功時に何も出力しないコマンドへ JSON envelope を要求していた｡修正後､`simulate --send`
で送信まで通ることを確認した｡この経緯は
`internal/herdrcli/void_test.go` に回帰テストとして残してある｡

同じ日に､既定の読み取り元が pane の viewport に制限されることも判明した
([pane-io.md](pane-io.md) の「読み取り元」参照)｡どちらも実際に動かすまで
気づけなかった類の失敗で､`--dry-run` や `simulate` だけでは踏めなかった｡

## 設定

`${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`｡ファイルが無ければ全て既定値｡
不正な値はそのキーだけ既定値に落ちる (タイプミス 1 つで監視が丸ごと止まらないようにするため)｡
デーモンは毎 tick 読み直すので､編集は数秒で反映される｡再起動は要らない｡

| キー | 既定 | 意味 |
| ---- | ---- | ---- |
| `enabled` | `true` | 全体スイッチ |
| `pollIntervalSeconds` | `5` | 各 pane を見る間隔 |
| `marginSeconds` | `60` | 解除時刻に足す余裕 |
| `fallbackWaitHours` | `5` | 解除時刻がどこからも取れないときの待ち |
| `maxRetries` | `5` | クールダウンに入るまでの再開試行回数 |
| `retryMessage` | `Continue where you left off.` | 再開時に送る文面 |
| `readSource` | `recent-unwrapped` | herdr の画面取得ソース｡`recent` / `recent-unwrapped` / `visible` |
| `readLines` | `25` | 1 回に読む行数 |
| `detectionTailLines` | `15` | 判定に使う末尾行数 |
| `usedPercentageThreshold` | `90` | ゲートの閾値 (これ未満なら発火しない) |
| `stateMaxAgeSeconds` | `900` | ゲート判定で使用率を信用する上限の古さ (`resets_at` には効かない) |
| `handleTransient` | `true` | 一時的なサーバーエラーも扱う |
| `transientWaitSeconds` | `60` | 一時エラーのバックオフ基準 |
| `transientMaxWaitSeconds` | `300` | バックオフの上限 |
| `dismissMenu` | `true` | 送信前に Escape を送る |
| `menuDismissDelayMs` | `300` | Escape の後の待ち |
| `submitDelayMs` | `400` | テキストと Enter の間の待ち |
| `engagedLabel` | `retry engaged` | 待機中に pane へ出すラベル |
| `customPatterns` | `[]` | 上限として扱う追加の正規表現 |
| `customTransientPatterns` | `[]` | 一時エラーとして扱う追加の正規表現 |
| `idleShutdownMinutes` | `30` | Claude pane が 0 のまま続いたら自己終了する |
| `deferToNativeAutoContinue` | `true` | ネイティブの auto-continue が再開を予約している間は手を出さない |
| `compactStallEnabled` | `true` | compact 直後に止まった pane を突く |
| `compactStallQuietSeconds` | `120` | 画面が変わらないまま何秒で「止まっている」と見なすか (10 未満は既定へ落ちる) |
| `compactStallCooldownMinutes` | `10` | 同じ pane を続けて突かない間隔 |
| `compactStallMaxNudges` | `3` | 1 回の停止 (送っても画面が変わらず続く状態) へ送る回数の上限｡超えたら送らずログに `compact-nudge-capped` を残し､pane は入力待ちのまま人の判断に委ねる｡応答が流れて停止が解けたら数え直す (1 未満は既定へ落ちる) |
| `compactStallMessage` | 下の「送る文面」 | compact 後の再開で送る文面 (marker 経路と画面判定の両方で使う) |
| `compactAutoEnabled` | `false` | context 使用率から `/agents-daemon:compact-prep` → `/compact` を投入する (使うなら config.json で `true` にする) |
| `compactAutoThresholdPercent` | `75` | この使用率以上で投入を始める (cache read が安い世代では早い compact が割に合わないため遅め) |
| `compactAutoPrepMessage` | `/agents-daemon:compact-prep` | 1 段目に送る文字列｡plugin の skill は名前空間付きで呼ぶ |
| `compactAutoMessage` | 下の「送る文面」 | 2 段目に送る文字列 (`/compact` 本体) |
| `compactAutoPrepTimeoutMinutes` | `5` | state file を待つ上限 |
| `compactAutoCooldownMinutes` | `15` | 1 段目を再投入しない間隔 |
| `compactResumeEnabled` | `true` | 圧縮完了 marker を見て作業を再開させる |
| `compactResumeDelaySeconds` | `60` | marker がこの秒数より古くなってから送る (10 未満は既定へ落ちる) |
| `cacheIdleCompactEnabled` | `true` | idle な pane の prompt cache が失効する前に compact-prep → `/compact` を投入する ([compact.md](compact.md) の「idle compact」)｡`compactAutoEnabled` とは独立 |
| `cacheIdleCompactLeadSeconds` | `600` | 失効の何秒前から動くか (60 以上 3600 未満｡範囲外は既定へ落ちる) |
| `cacheIdleCompactThresholdPercent` | `40` | この使用率以上で動く |

### 送る文面

既定値は `internal/config/config.go` の `default*Message` 定数｡
どれも上限の検知語 (`limit` / `rate` 等) を含めてはいけない ([pane-io.md](pane-io.md))｡

`compactStallMessage` (既定):

```text
Continue the task you were working on. Reread the plan file or the compact-prep state file for the open items and work through them; if one is blocked, say what is blocking it.
```

daemon は残項目そのものを知らないので､名指しの代わりに plan file / state file を読み直させる｡

`compactAutoMessage` (既定):

```text
/compact Keep the plan file path, current phase, and unresolved items; keep decisions made, options tried or set aside and why, and constraints in their exact wording.
```

末尾の指示文は圧縮サマリーの保持リスト｡外すと `/compact` が compact-prep の skill と前方一致で
補完メニューを開き､Enter が候補を横取りしうる ([compact.md](compact.md))｡

`compactAutoEnabled` だけ既定を `false` にしてある｡`/compact` は取り消せないため､
コードの既定は投入しない側へ倒す｡有効化は config.json で行う｡
`cacheIdleCompactEnabled` は既定 `true`｡warm なうちに要約しておけば失効後の書き直しが要約だけで済み､
圧縮後に再開も送らない ([compact.md](compact.md) の「idle compact」)｡止めるなら config.json で `false` にする｡
圧縮**後**の再開 (`compactResumeEnabled`) は安全なので既定 `true`｡

Claude Code の画面の文言は安定した API ではない｡変わって検知が止まったら
`customPatterns` に新しい表現を足せば､コードを変えずに追随できる｡

## 既知の制約

- **起こし方によっては `stop` の SIGTERM が届かない｡** SessionStart hook から起動された
  デーモンには届く (実測)｡一方､エージェントセッションの Bash ツールがバックグラウンド
  ジョブとして起こしたデーモンへは `operation not permitted` で弾かれる｡デーモン側の
  シグナル処理・PID ファイルの後始末・終了ログはいずれも正常なので､落ちているのは
  送信側だけ｡弾かれたら通常のターミナルから実行するか､`idleShutdownMinutes` による
  自己終了に任せる｡
- **plugin を有効にしていない環境では動かない｡** hook が配線されないため､デーモンが起きない｡
  config.json だけ置いても何も起きない｡
- **Go が無いとバイナリが建たない｡** SessionStart hook は `go build` でバイナリを用意する｡
  `go` が PATH に無ければ `$STATE/logs/build.log` に ERROR を残し､デーモンは起きない｡
- **herdr が要る｡** 到達できないときデーモンは起動しない｡Ghostty 単体では pane へ
  キーを送る手段が無いため､この機能は成立しない｡
- **statusline を切っていると state ファイルが作られない｡** statusline はこの plugin に含まれない｡
  利用者の statusline が stdin を `agents-daemon ingest-statusline` へ渡さなければ､ゲートは働かず､
  待機時刻は画面かフォールバックから決まり､compact の自動投入は動かない｡
- **herdr が pane にセッション ID を紐づけていないと own state を引けない｡** 対応する
  ファイルが決まらないためゲートは適用されず､画面の分類だけで判定する｡起床時刻は
  画面の絶対時刻かアカウント全体の state から決まる｡
- **上限中に開いた新規セッションは､プロンプトを打って弾かれていないと再開しても
  始まらない｡** `retryMessage` は「続き」を頼む文面で､何も無いセッションでは
  続きが無い｡弾かれたプロンプトがあればそれが会話に残っているので進む｡
- **ネイティブの auto-continue が再開を予約している間は何もしない｡** 既定ではそちらに任せる
  (`deferToNativeAutoContinue`)｡ネイティブが降りた画面から改めて拾う｡
- **spend limit / usage credit では待機に入らない｡** 待っても解除されないため｡
  `/usage-credits` での引き上げは人間の操作なので､このツールは何もしない｡
- **pane の中で tmux が動いていると送信が入力行へ届かない｡** agent teams
  (`tmux -L claude-swarm-*`) を開いたセッションの pane に `send-text` すると､
  文字列は tmux 側でフォーカスを持つペイン (teammate 一覧など) に吸われ､Claude の
  入力行は空のままになる (実測)｡上限からの再開も compact の自動化も同じ経路を通るため､
  この形の pane では届かない｡
- **compact の自動化は herdr の pane に限られる｡** daemon は `herdr pane list` で
  セッションを見つけるため､素のターミナルで動く Claude Code には何も届かない｡
  そちらは Claude Code 本体の autocompact (90% 付近) に任せることになる｡
- **長く放置した pane では 1 段目が動かない｡** 使用率は statusline の描画で更新される
  ため､最後の描画から `stateMaxAgeSeconds` (既定 15 分) を過ぎた pane は「観測が古い」
  として見送る｡実際に効くのはターンが終わった直後 — 圧縮を挟みたい場所そのもの — で､
  数時間放置した pane は次のターンまで待つことになる｡
- **compact 停止の画面判定には裏取りが 1 つしか無い｡** 上限検知のような二重シグナルが
  取れないので､画面の安定・`agent_status`・クールダウンで代替している｡
  誤って突かれるのが嫌なら `compactStallEnabled` を false にする
  (marker 経路の再開は `compactResumeEnabled` で別に制御する)｡
- **`visible` を選ぶと pane の viewport に制限される｡** 既定の `recent-unwrapped` は
  スクロールバックから取るので影響を受けないが､`visible` に変えると表示領域に
  収まる範囲しか返らず､判定に必要な行が溢れる｡詳細は
  [pane-io.md](pane-io.md) の「読み取り元」を参照｡
- **待機中のラベルは `--state-label` で出す｡** herdr 0.8.0 に `--custom-status` は存在しない｡
