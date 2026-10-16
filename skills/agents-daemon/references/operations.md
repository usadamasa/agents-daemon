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

### plugin の更新後に daemon を入れ替える

`claude plugin update` はバイナリを建て直さない｡建て直すのは次の SessionStart hook で､
それまで動いている daemon は古い版のまま｡セッションを開き直さずに入れ替えるには､install 済み plugin の
`hooks/build-daemon.sh` を直接叩く (SessionStart が detach して呼ぶのと同じスクリプト)｡

```sh
"$(jq -r '.plugins["agents-daemon@usadamasa"][0].installPath' ~/.claude/plugins/installed_plugins.json)/hooks/build-daemon.sh"
```

plugin の版で `go build` し､cache のバイナリと stamp を差し替えて `daemon --ensure` を叩く｡動いている daemon は
差し替えを次の tick で検知して自分で入れ替わる ([architecture.md](architecture.md) の「バイナリの用意と差し替え」)｡
`status` の `started_at` が進んでいれば済んでいる｡Claude Code の sandbox は `~/.cache/agents-daemon` に書けないので､
利用者が `!` で打つ｡

### 検証用コマンド

```sh
agents-daemon doctor                    # 前提条件の診断
agents-daemon inspect --pane <id>       # 生きた pane の画面を分類する
agents-daemon inspect --file <path>     # 保存した画面テキストを分類する
agents-daemon inspect --file <path> --session <id>  # ゲート判定に使うセッションを指定する
agents-daemon simulate                  # 使い捨て pane で検知から送信まで通す
agents-daemon simulate --send --keep    # 実際に送信し､pane を残して目で見る
```

`statusLine.command` から呼ばれる `statusline` は stdin の JSON を受けて state file を書き､描画を stdout に出す｡
手で叩くなら `COLUMNS=200 agents-daemon statusline < in.json` で渡す (COLUMNS が無ければ full の 3 行)｡
自前の statusline から呼ぶ `ingest-statusline` は state file を書くだけで stdout には何も出さない｡
どちらも失敗しても exit 0 で終わる (理由は stderr)｡

`inspect` は判定結果だけでなく､走査した行・ゲートが開いているか・待機時刻をどの
情報源から得たかを出す｡検知がおかしいと疑ったときに､結論ではなく判断過程を見るためのもの｡

ゲート判定には「どのセッションの state を見るか」が要る｡`--pane` を指定したときは
pane から解決する｡`--file` にはセッションが無いため `--session` で明示する｡
指定が無ければゲート判定を行わない旨を出力して､画面の分類だけを報告する｡

`simulate` は自分の pane を分割して使い捨ての pane を作り､Claude agent に見せかけ､limit 風の画面を
書き込んで､デーモンと同じ判定を通す｡既定では送信しない｡自分が作った pane 以外には
決して触らない｡backend は daemon と同じ規則 (`REX_SESSION` の有無) で選ぶ｡

使い捨て pane の前景は両 backend とも `/bin/sh <script> <screen>`｡script は limit 風の画面を cat したあと
`exec cat -v` で居座り､daemon が送る esc・文字列・enter を解釈せずそのまま画面に書く (ESC は `^[`)｡
shell を受け手にすると ESC の直後の文字を Meta として食う (zsh の `KEYTIMEOUT` 0.4 秒) など shell の解釈が混ざり､
`--send` で何が届いたかを画面で読めない｡

| backend | 自分の pane | script の起動 | Claude agent に見せる手段 | 後始末 |
| ---- | ---- | ---- | ---- | ---- |
| herdr | `HERDR_ENV=1` のセッション | `pane split` は shell しか開けないので､起動を待って `pane run <id> /bin/sh …` と enter を打つ | `herdr pane report-agent` (agent=claude, state=idle) | `release-agent` と `pane close` |
| rex | `REX_BLOCK` の block | `rex split --shell=none -- /bin/sh …` で shell を挟まず直接 | script の先頭で Claude Code と同じ OSC 7501 (`state=idle:app=claude-code`) を `printf` し､`program_status` の record に載せる | `rex block close` |

どちらも登録が backend に載ったことを daemon と同じ経路 (`PaneGet` → `IsClaudeAgent`) で待ってから画面を読む｡
rex の `capture` は可視領域しか返さないので､使い捨て block は自分の block の 40% の高さで割る｡
Claude Code の OSC 7501 の書式は 2.1.296 のバイナリから読んだもの｡対応そのものは CHANGELOG 2.1.295 が
「Program Status Protocol (OSC 7501)」として名指ししたが､payload の書式を書いた docs のページはまだ無い
(「rex の状態の対応」参照)｡

`doctor` は backend (herdr / rex) と選んだ根拠 (`REX_SESSION` の有無)､その server への到達性､Claude pane の
一覧 (`session_id` が引けなければ `(なし)`) を出す｡daemon の `status.json` の backend が今の環境と違えば
警告する｡末尾の「peer messaging」は `CLAUDE_CODE_MESSAGING_SOCKET` と socket の有無で､Claude Code の
セッション内 (Bash ツール) から呼んだときだけ意味を持つ (切り分けは `agents-daemon:setup` skill の
`references/peer-messaging.md`)｡`inspect --pane` の pane ID は rex では block ID｡

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

### rex の状態の対応

2026-10-10､rex `0.0.1791570264-g7b5bf3` と Claude Code `2.1.296` で測った｡rex では Claude Code 自身が
OSC 7501 で状態を報告し､daemon は `block call -b BLOCK program_status` の `records` から読む｡
対応は CHANGELOG 2.1.295 (Program Status Protocol) にあるが､payload の書式は公式ドキュメントに無いので､
版が変われば測り直す｡

<!-- cc-premise: 2.1.296 osc7501 state / app / kind の値と画面の状況の対応 -->


使うのは root record (`id` が無いか空) だけで､`id` に agent ID を持つ record は subagent のもの｡

| 画面の状況 | `state` | `app` | `agent_status` |
| ---- | ---- | ---- | ---- |
| 作業中 | `working` | `claude-code` | `working` |
| ターンを終えて利用者の入力待ち | `done` | `claude-code` | `done` |
| permission prompt を Esc で断った後 | `idle` | `claude-code` | `idle` |
| permission prompt (`kind` は `permission`､`msg` は `approve <tool>: <command>`) | `blocked` | `claude-code` | `blocked` |
| 起動直後の trust dialog (`kind` も `msg` も無し) | `blocked` | `claude` | `blocked` |
| 上のどれでもない値 | その他 | - | `unknown` |
| shell だけの block | `records: []` | - | Claude agent ではない |

- `app` が `claude` で始まれば Claude agent と見なす｡record が無くても前景プロセスの `argv0` が `claude` なら
  Claude agent とし､`agent_status` は `unknown`｡
- OSC 7501 の payload は `key=value` を `:` で繋いだもの (`state=idle:app=claude-code`､`title` と `msg` は base64)｡
  終端は BEL｡`simulate` はこれをシェルの `printf` で出して使い捨て block を Claude に見せる｡
- `done` と `idle` はどちらも入力待ち｡`blocked` は利用者の操作待ちで､daemon は打ち込まない (herdr と同じ扱い)｡

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
| `readSource` | `recent-unwrapped` | herdr の画面取得ソース｡`recent` / `recent-unwrapped` / `visible`｡rex では無視 (可視領域だけを読む) |
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
- **herdr か rex が要る｡** 選ばれた backend の server に到達できないときデーモンは起動しない｡
  Ghostty 単体では pane へキーを送る手段が無いため､この機能は成立しない｡
- **daemon は 1 つの backend しか見ない｡** 選ぶのは起動時の環境 (`REX_SESSION` の有無) で､herdr の
  セッションから起きた daemon は rex の block を見ない (逆も同じ)｡`doctor` の daemon の節が警告する｡
  `agents-daemon stop` の後､見たい側のセッションで起こし直す｡
- **statusline を切っていると state ファイルが作られない｡** `statusLine.command` がバイナリの `statusline` を
  指しているか､利用者の statusline が stdin を `agents-daemon ingest-statusline` へ渡していなければ､
  ゲートは働かず､待機時刻は画面かフォールバックから決まり､compact の自動投入は動かない｡
- **pane にセッション ID が紐づいていないと own state を引けない｡** herdr も rex も `context/<sid>.json` の
  `multiplexer` (statusline が環境変数の `HERDR_PANE_ID` / `REX_BLOCK` から書く) で紐づける｡statusline が
  配線されていないと session_id が引けない (`doctor` で `session_id=(なし)`)｡対応する
  ファイルが決まらないためゲートは適用されず､画面の分類だけで判定する｡起床時刻は
  画面の絶対時刻かアカウント全体の state から決まる｡herdr の pane が持つ `agent_session` (herdr の
  integration script が入れる値) は読まない｡
- **herdr で pane を別の workspace へ `pane move` すると､その pane の対応が切れる｡** herdr は移動した pane に
  新しい workspace 付きの ID を振り (`agents-daemon:herdr` skill の「Use IDs and caller context」)､`pane list` には新しい ID だけが
  出る｡一方 sidecar に書かれるのは Claude Code が起動時に継承した古い `HERDR_PANE_ID` のまま｡
  `doctor` で `session_id=(なし)` になり､その pane では own state が引けないので上限の再開のゲートと
  compact の自動投入が効かない (画面の分類だけで動く)｡戻すには､その pane で Claude Code を起動し直す
  (新しい ID で記録される)｡同じ workspace 内の `pane move` (タブ間) は ID が変わらないはずなので影響しない
  (`agents-daemon:herdr` skill の記述と `w1:p1` の形からの推測で､実測はしていない)｡
  herdr の再起動で pane ID が変わるかも未測定 (herdr は ID を「stable handles」と言う)｡
  以前読んでいた `agent_session` は herdr が pane に付けて持っていたので､移動しても追えていた｡
  別 workspace への `pane move` を日常的に使うなら､この経路の制約として知っておく｡
- **SessionStart から statusline の初回描画までの数秒は対応が無い｡** sidecar は statusline が書くため｡
  daemon は tick ごとに引き直すので､その後の tick から見える｡
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
- **compact の自動化は herdr の pane か rex の block に限られる｡** daemon は多重化ツールの一覧で
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
- **rex は常に可視領域だけを読む｡** `readSource` は効かず､scrollback を読む手段が無い｡
  小さい block では上限の文言が流れた時点で検知が効かなくなる ([pane-io.md](pane-io.md) の「rex の読み取り元」)｡
- **待機中のラベルは `--state-label` で出す｡** herdr 0.8.0 に `--custom-status` は存在しない｡
- **rex では待機中のラベルが出ない｡** `ReportMetadata` は rex では何もしない｡表示に使える `block rename` は
  利用者が付けた label を潰すため｡待機の状況は `agents-daemon status` で見る｡
- **rex の送信は実機で測っていない｡** `send` / `send-key` の組み立ては fake の rex で確かめただけで､
  daemon が渡すキー名 (`esc` `enter`) は herdr のもの｡rex でそのまま通るかは未測定｡
- **rex の状態は Claude Code の OSC 7501 の報告に頼る｡** 対応は CHANGELOG 2.1.295 にあるが payload の書式は
  公式ドキュメントに無いので､Claude Code の版で変わりうる｡測った版は上の「rex の状態の対応」｡
- rex 側の制約 (可視領域だけ､ラベルが出ない､送信が未測定) が今の版でも残っているかは､
  `agents-daemon:peer-sessions` skill の `references/rex.md` の「使い方・不具合・新機能の情報源」で
  docs と feedback を引いて確かめる｡
