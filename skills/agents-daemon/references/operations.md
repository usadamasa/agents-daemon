# agents-daemon のコマンド・設定・制約

## コマンド

バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` にある。PATH には入れていないので、
手で叩くときは絶対パスで呼ぶか、そのディレクトリを PATH に足す。

```sh
agents-daemon daemon --ensure [--dry-run]      # 居なければ detach 起動 (hook 用)
agents-daemon daemon --foreground [--dry-run]  # このプロセスで回す (デバッグ用)
agents-daemon status                           # 稼働状況、監視中の pane、ログの末尾
agents-daemon logs -n 80                       # 現行ログ (daemon.log) の末尾
agents-daemon logs --board [--session <id>]    # ボードの経路 (タスクの記録、パーキング、mod) のログの末尾。cwd の domain の分だけ
agents-daemon stop                             # SIGTERM を送って停止
agents-daemon dream harvest --incremental      # 直近の transcript を集める (Stop hook が --detach 付きで呼ぶ)
agents-daemon dream mine|stage|adopt|forget    # 記憶階層の consolidation の実行系 (agents-daemon:dream skill)
agents-daemon prune-worktrees [--dry-run]      # 放置された Claude Code の worktree を回収する (SessionStart hook が --hook 付きで呼ぶ)
agents-daemon edge add|rm|list                 # セッションをまたぐ作業の依存を書く・消す・一覧する (下の「セッションをまたぐ辺を書く」)
```

### `--dry-run`

検知も判定も本番と同じ経路で行い、**pane への送信とラベル更新だけをしない**。
「本来なら何を送っていたか」がログに残る。

稼働中のセッションへ誤ってプロンプトを打ち込む事故は取り返しがつかないため、
送信経路そのものを塞げる状態を用意してある。`--ensure` から detach 起動するときも
このフラグは引き継がれる (引き継がないと `--ensure --dry-run` が実際には送信する
デーモンを生む、という最も危険な取り違えになる)。

SessionStart hook は `--dry-run` **なし**で配線してある。検知の挙動を疑ったときや、
設定を変えて様子を見たいときに、手動で `--foreground --dry-run` を回す使い方を想定している。

### plugin の更新後に daemon を入れ替える

`claude plugin update` はバイナリを建て直さない。建て直すのは次の SessionStart hook で、
それまで動いている daemon は古い版のまま。セッションを開き直さずに入れ替えるには、install 済み plugin の
`hooks/build-daemon.sh` を直接叩く (SessionStart が detach して呼ぶのと同じスクリプト)。

```sh
"$(jq -r '.plugins["agents-daemon@usadamasa"][0].installPath' ~/.claude/plugins/installed_plugins.json)/hooks/build-daemon.sh"
```

plugin の版で `go build` し、cache のバイナリと stamp を差し替えて `daemon --ensure` を叩く。動いている daemon は
差し替えを次の tick で検知して自分で入れ替わる ([architecture.md](architecture.md) の「バイナリの用意と差し替え」)。
`status` の `started_at` が進んでいれば済んでいる。Claude Code の sandbox は `~/.cache/agents-daemon` に書けないので、
利用者が `!` で打つ。

### 放置された worktree を回収する

普段は SessionStart hook が `prune-worktrees --hook` で、今開いたリポジトリの `.claude/worktrees/` 配下を回収する
(回収したときだけ systemMessage が出る)。今すぐ回収したい、しきい値を変えたい、未コミットの変更ごと消したいときは
リポジトリの中で直に叩く。

```sh
~/.cache/agents-daemon/bin/agents-daemon prune-worktrees --dry-run          # 対象を表示するだけ
~/.cache/agents-daemon/bin/agents-daemon prune-worktrees                    # 7 日以上触っていないものを回収
~/.cache/agents-daemon/bin/agents-daemon prune-worktrees --min-age-days 0   # 作りたてでも回収
~/.cache/agents-daemon/bin/agents-daemon prune-worktrees --force            # 未コミットの変更があっても回収
```

回収するのは、lock が無いか foreground セッションの lock の PID が不在で、最終更新 (ルートの mtime・HEAD の
コミット日時・管理 index の mtime の最大) が `--min-age-days` より古く、未コミットの変更が無いもの。ブランチは残す。
自分が中にいる worktree、人手の lock、subagent の lock (`claude agent`。本体の sweep の担当) には触らない。

Claude Code の sandbox の中で叩くと、`git worktree remove` は main worktree の `.git/worktrees/` と兄弟の worktree
ディレクトリに書く。worktree の中から叩くと cwd の外への書き込みになるので、sandbox の書き込み許可
(`sandbox.filesystem.allowWrite`) にリポジトリの親ディレクトリが入っていないリポジトリでは `operation not permitted`
になる。その場合は利用者が `!` で打つか、main worktree を cwd にして叩く。

### セッションをまたぐ辺を書く

作業を別のセッションへ渡す側 (workspace の skill など) が、渡すときに「何の後か」を書く (#134)。辺は
「`--before` が済まないと `--after` を始められない」を表す。結果は JSON で stdout に出る。

```sh
agents-daemon edge add \
  --before session:ad-135 --before-cwd ~/src/github.com/usadamasa/agents-daemon \
  --after issue:usadamasa/agents-daemon#143 --after-cwd ~/src/github.com/usadamasa/agents-daemon \
  --note "段 4 のリリースの後"                       # {"created": true, "edge": {...}}
agents-daemon edge rm --before ... --before-cwd ... --after ... --after-cwd ...   # {"removed": true, "id": "..."}
agents-daemon edge list --cwd <dir> [--here | --repo <main worktree>] [--ref <参照>]
```

- 参照は `session:<セッション名>` (`claude --name` の名前)、`issue:<owner>/<repo>#<番号>`、`pr:<owner>/<repo>#<番号>`、
  `task:<session_id>/<task_id>` のどれか
- `--before-cwd` / `--after-cwd` は、その作業のセッションを起こしたディレクトリ (絶対パス)。両端の domain (私用か業務) は
  ここから決め、違えば書かずに失敗する。書く側のセッション自身の cwd は見ない
- 同じ参照の組 (向きを含む) は同じ辺になる。登録し直すと note と repo を書き直し、保持期間 (7 日) を数え直す
- `list` は `--cwd` (と `--session`) の domain の辺だけを出す。`--repo` / `--here` / `--ref` はどちらかの端点で絞る

### 検証用コマンド

```sh
agents-daemon doctor                    # 前提条件の診断
agents-daemon inspect --pane <id>       # 生きた pane の画面を分類する
agents-daemon inspect --file <path>     # 保存した画面テキストを分類する
agents-daemon inspect --file <path> --session <id>  # ゲート判定に使うセッションを指定する
agents-daemon simulate                  # 使い捨て pane で検知から送信まで通す
agents-daemon simulate --send --keep    # 実際に送信し、pane を残して目で見る
```

`statusLine.command` から呼ばれる `statusline` は stdin の JSON を受けて state file を書き、描画を stdout に出す。
手で叩くなら `COLUMNS=200 agents-daemon statusline < in.json` で渡す (COLUMNS が無ければ full の 3 行)。
自前の statusline から呼ぶ `ingest-statusline` は state file を書くだけで stdout には何も出さない。
どちらも失敗しても exit 0 で終わる (理由は stderr)。

`inspect` は判定結果だけでなく、走査した行・ゲートが開いているか・待機時刻をどの
情報源から得たかを出す。検知がおかしいと疑ったときに、結論ではなく判断過程を見るためのもの。

ゲート判定には「どのセッションの state を見るか」が要る。`--pane` を指定したときは
pane から解決する。`--file` にはセッションが無いため `--session` で明示する。
指定が無ければゲート判定を行わない旨を出力して、画面の分類だけを報告する。

`simulate` は自分の block を分割して使い捨ての block を作り、Claude agent に見せかけ、limit 風の画面を
書き込んで、デーモンと同じ判定を通す。既定では送信しない。自分が作った block 以外には
決して触らない。`REX_SESSION` と `REX_BLOCK` が要る。

使い捨て block の前景は `/bin/sh <script> <screen>`。script は limit 風の画面を cat したあと
`exec cat -v` で居座り、daemon が送る esc・文字列・enter を解釈せずそのまま画面に書く (ESC は `^[`)。
shell を受け手にすると ESC の直後の文字を Meta として食う (zsh の `KEYTIMEOUT` 0.4 秒) など shell の解釈が混ざり、
`--send` で何が届いたかを画面で読めない。

| 自分の block | script の起動 | Claude agent に見せる手段 | 後始末 |
| ---- | ---- | ---- | ---- |
| `REX_BLOCK` の block | `rex split --shell=none -- /bin/sh …` で shell を挟まず直接 | script の先頭で Claude Code と同じ OSC 7501 (`state=idle:app=claude-code`) を `printf` し、`program_status` の record に載せる | `rex block close` |

登録が rex に載ったことを daemon と同じ経路 (`PaneGet` → `IsClaudeAgent`) で待ってから画面を読む。
rex の `capture` は可視領域しか返さないので、使い捨て block は自分の block の 40% の高さで割る。
OSC 7501 の書式と判定の条件は [program-status.md](program-status.md)。

`doctor` は rex の server への到達性、Claude pane の一覧 (`session_id` が引けなければ `(なし)`) を出す。
rex の外 (`REX_SESSION` なし) では exit 1 で止まる。「rex CLI の前提」は自分の block (`REX_BLOCK`) に固定の
コマンドを打ち、コードが読む欄と exit code が rex の版で変わっていないかを `⚠` で名指しする (rex を上げた直後に
全 pane が unknown になったときの最初の手がかり)。末尾の「peer messaging」は
`CLAUDE_CODE_MESSAGING_SOCKET` と socket の有無で、Claude Code のセッション内 (Bash ツール) から呼んだときだけ
意味を持つ (切り分けは `agents-daemon:setup` skill の `references/peer-messaging.md`)。
`inspect --pane` の pane ID は block ID。

### 実機での検証順序

誤検知しないことの確認を先に置く。検知できることの確認はその後。

1. `task install` (リポジトリの clone で、hook と同じ cache のパスへ建てる)
2. `doctor` — 前提が揃っているか
3. `inspect --pane <自分の pane>` — 通常の画面が `none` と判定されること
4. `inspect --file <limit 画面のサンプル>` — `limit` と判定され、待機時刻が `resets_at` 由来であること
5. `daemon --foreground --dry-run` — 実 pane 相手に流し、誰にも送信せず誤爆もしないこと
6. `simulate` — 検知から送信までを end-to-end で通す

### この環境で実際に確認したこと

2026-08-16、Ghostty + herdr 0.8.0 (当時の backend。herdr のサポートは #66 で廃止) で通した結果。

- 開発中のセッション自身が 5 時間上限に到達し、本物の表示を取得した
  (`You've hit your session limit · resets HH:MMpm (Asia/Tokyo)`、時刻は上記の理由で伏せてある)。
  この文言に対する分類は `internal/detect/real_sample_test.go` に固定してある。
- 同じ事象での `resets_at` は `1786851000` = 12:30 JST で、画面表示と一致した。
- SessionStart hook からデーモンが自動起動することを確認。
- `idle` の Claude セッションを誤検知しない。
- 画面が limit に見えるが使用率が閾値未満の pane で、ゲートが実際に発火を抑制した。
- 別ワークスペースの pane も監視対象に入る。
- `simulate` が検知 → 待機 → 再開送信 (esc / テキスト / enter) まで通した。

そして本番投入した当日、2 つのセッションが実際に上限へ到達し、**自動再開は失敗した**。
待機のタイマーは正しく動いた (`resets_at` 17:50 + margin 60 秒 → 17:51 ちょうどに起床) が、
送信が毎回エラーになり再試行上限まで進んだ。原因は herdr の応答の解釈違いで、
成功時に何も出力しないコマンドへ JSON envelope を要求していた。修正後、`simulate --send`
で送信まで通ることを確認した。教訓は「送信の経路は fake ではなく実機で 1 度は通す」で、
rex の送信が未測定なのはこの教訓がまだ果たされていない箇所 (下の「既知の制約」)。

同じ日に、既定の読み取り元が pane の viewport に制限されることも判明した
([pane-io.md](pane-io.md) の「読み取り元」参照)。どちらも実際に動かすまで
気づけなかった類の失敗で、`--dry-run` や `simulate` だけでは踏めなかった。

## 設定

`${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`。ファイルが無ければ全て既定値。
不正な値はそのキーだけ既定値に落ちる (タイプミス 1 つで監視が丸ごと止まらないようにするため)。
デーモンは毎 tick 読み直すので、編集は数秒で反映される。再起動は要らない。

| キー | 既定 | 意味 |
| ---- | ---- | ---- |
| `enabled` | `true` | 全体スイッチ |
| `pollIntervalSeconds` | `5` | 各 pane を見る間隔 |
| `marginSeconds` | `60` | 解除時刻に足す余裕 |
| `fallbackWaitHours` | `5` | 解除時刻がどこからも取れないときの待ち |
| `maxRetries` | `5` | クールダウンに入るまでの再開試行回数 |
| `retryMessage` | `Continue where you left off.` | 再開時に送る文面 |
| `readLines` | `25` | 1 回に読む行数 (可視領域の末尾から) |
| `detectionTailLines` | `15` | 判定に使う末尾行数 |
| `usedPercentageThreshold` | `90` | ゲートの閾値 (これ未満なら発火しない) |
| `stateMaxAgeSeconds` | `900` | ゲート判定で使用率を信用する上限の古さ (`resets_at` には効かない) |
| `handleTransient` | `true` | 一時的なサーバーエラーも扱う |
| `transientWaitSeconds` | `60` | 一時エラーのバックオフ基準 |
| `transientMaxWaitSeconds` | `300` | バックオフの上限 |
| `dismissMenu` | `true` | 送信前に Escape を送る |
| `menuDismissDelayMs` | `300` | Escape の後の待ち |
| `submitDelayMs` | `400` | テキストと Enter の間の待ち |
| `customPatterns` | `[]` | 上限として扱う追加の正規表現 |
| `idleShutdownMinutes` | `30` | Claude pane が 0 のまま続いたら自己終了する |
| `deferToNativeAutoContinue` | `true` | ネイティブの auto-continue が再開を予約している間は手を出さない |
| `homePaths` | `[]` | 私用と判定するパス (絶対パスか `~/` 始まり)。cwd がこの下なら私用 |
| `defaultDomain` | `work` | `homePaths` に当たらない cwd の区分 (`work` か `home`) |

旧版の compact / cache のキー (`compactStall*` `compactAuto*` `compactResume*` `cacheIdleCompact*`) は
読まない。config.json に残っていても他のキーには影響しないが、消してよい。

### 私用と業務の区分

セッションの cwd から、私用 (`home`) と業務 (`work`) のどちらの作業かを決める (`internal/domain`)。
これから入るタスクの記録とパーキングロットは区分ごとに分けて置き、ボードも 1 つの区分しか描かない (#121)。
業務のものをインターネットに出さず、私用と混ぜないための境界になる。

私用と言えるのは `homePaths` の下だけで、それ以外と判定できないもの (cwd が空、相対パス、
`defaultDomain` の typo) はすべて業務に倒す。判定を誤っても、業務のものが私用の側に漏れない向きにするため。

| 端末 | 書くもの |
| ---- | ---- |
| 業務の端末 (業務と私用の両方を触る) | `"homePaths": ["~/src/github.com/usadamasa"]` のように私用のリポジトリの置き場だけを書く |
| 私用の端末 (私用だけを触る) | `"defaultDomain": "home"`。パスに関係なくすべて私用になる |

書いたら `doctor` の「domain (私用と業務の境界)」で、Claude の pane ごとの判定を確かめる。

### 送る文面

既定値は `internal/config/config.go` の `defaultRetryMessage`。
上限の検知語 (`limit` / `rate` 等) を含めてはいけない ([pane-io.md](pane-io.md))。

Claude Code の画面の文言は安定した API ではない。変わって検知が止まったら
`customPatterns` に新しい表現を足せば、コードを変えずに追随できる。

## 既知の制約

- **起こし方によっては `stop` の SIGTERM が届かない。** SessionStart hook から起動された
  デーモンには届く (実測)。一方、エージェントセッションの Bash ツールがバックグラウンド
  ジョブとして起こしたデーモンへは `operation not permitted` で弾かれる。デーモン側の
  シグナル処理・PID ファイルの後始末・終了ログはいずれも正常なので、落ちているのは
  送信側だけ。弾かれたら通常のターミナルから実行するか、`idleShutdownMinutes` による
  自己終了に任せる。
- **plugin を有効にしていない環境では動かない。** hook が配線されないため、デーモンが起きない。
  config.json だけ置いても何も起きない。
- **Go が無いとバイナリが建たない。** SessionStart hook は `go build` でバイナリを用意する。
  `go` が PATH に無ければ `$STATE/logs/build.log` に ERROR を残し、デーモンは起きない。
- **rex が要る。** rex の server に到達できないときデーモンは起動せず、`REX_SESSION` の無いセッションの
  SessionStart では起こさない。Ghostty 単体では pane へキーを送る手段が無いため、この機能は成立しない。
- **statusline を切っていると state ファイルが作られない。** `statusLine.command` がバイナリの `statusline` を
  指しているか、利用者の statusline が stdin を `agents-daemon ingest-statusline` へ渡していなければ、
  ゲートは働かず、待機時刻は画面かフォールバックから決まる。
- **block にセッション ID が紐づいていないと own state を引けない。** `context/<sid>.json` の
  `multiplexer` (statusline が環境変数の `REX_BLOCK` から書く) で紐づける。statusline が
  配線されていないと session_id が引けない (`doctor` で `session_id=(なし)`)。対応する
  ファイルが決まらないためゲートは適用されず、画面の分類だけで判定する。起床時刻は
  画面の絶対時刻かアカウント全体の state から決まる。
- **SessionStart から statusline の初回描画までの数秒は対応が無い。** sidecar は statusline が書くため。
  daemon は tick ごとに引き直すので、その後の tick から見える。
- **上限中に開いた新規セッションは、プロンプトを打って弾かれていないと再開しても
  始まらない。** `retryMessage` は「続き」を頼む文面で、何も無いセッションでは
  続きが無い。弾かれたプロンプトがあればそれが会話に残っているので進む。
- **ネイティブの auto-continue が再開を予約している間は何もしない。** 既定ではそちらに任せる
  (`deferToNativeAutoContinue`)。ネイティブが降りた画面から改めて拾う。
- **spend limit / usage credit では待機に入らない。** 待っても解除されないため。
  `/usage-credits` での引き上げは人間の操作なので、このツールは何もしない。
- **pane の中で tmux が動いていると送信が入力行へ届かない。** agent teams
  (`tmux -L claude-swarm-*`) を開いたセッションの pane に `send-text` すると、
  文字列は tmux 側でフォーカスを持つペイン (teammate 一覧など) に吸われ、Claude の
  入力行は空のままになる (実測)。この形の pane には再開が届かない。
- **rex は常に可視領域だけを読む。** scrollback を読む手段が無い。
  小さい block では上限の文言が流れた時点で検知が効かなくなる ([pane-io.md](pane-io.md) の「読み取り元」)。
- **待機中のラベルは出ない。** 表示に使える `block rename` は利用者が付けた label を潰すため。
  待機の状況は `agents-daemon status` で見る。
- **rex の送信は実機で測っていない。** `send` / `send-key` の組み立ては fake の rex で確かめただけで、
  daemon が渡すキー名 (`esc` `enter`) が rex でそのまま通るかは未測定。
- **rex の状態は Claude Code の OSC 7501 の報告に頼る。** どの状況でどの値が来るかは公式ドキュメントに無く、
  Claude Code の版で変わりうる。測った版と対応は [program-status.md](program-status.md)。
- **kind の無い `blocked` (Session paused などの dialog) が開いた pane にも再開を送る。** 送らないのは kind が
  `permission` / `question` / `auth` のときだけ。上限到達時の record が kind 無しの `blocked` で来るかを
  実測していないため、全部の `blocked` を止めると上限からの再開そのものが止まりかねない (#77)。
  上限を踏んだときの record は daemon.log の「待機に入りました」の行に `agent_status=… kind=… msg="…"` で残る。
- rex 側の制約 (可視領域だけ、ラベルが出ない、送信が未測定) が今の版でも残っているかは、
  `agents-daemon:peer-sessions` skill の `references/rex.md` の「使い方・不具合・新機能の情報源」で
  docs と feedback を引いて確かめる。
