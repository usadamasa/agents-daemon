# agents-daemon の全体構成と出力ファイル

## 全体の連動

statusline・hook・skill・daemon が別々のタイミングで動き､ファイルを介して繋がる｡
互いを直接呼ばない｡

```
[1] Claude Code ──毎描画──stdin──> agents-daemon statusline (statusLine.command)
                                   または 利用者の statusline ──stdin──> agents-daemon ingest-statusline
                                                   ├──> rate-limits/<session_id>.json (あれば)
                                                   ├──> context/<session_id>.json     (毎回)
                                                   └──> cost/<YYYY-MM-DD>/<session_id>.json (statusline だけ､上限の env があるとき)
    compact-prep skill ────────────────────> compact-state/<session_id>.md
    PostCompact hook ──────────────────────> compacted/<session_id>
    Claude Code ──非同期に追記──> transcript (context/ の transcript_path が指す)
                                                                          │
[2] SessionStart hook ──daemon --ensure──> daemon (常駐 1 プロセス)        │ pane の
                                              │                           │ session を
                                              │ 毎 tick (既定 5 秒) <──────┘ 引いて読む
                                              │
                                              ├─ PaneList ────────────> Claude の pane 一覧
                                              ├─ PaneRead ────────────> 各 pane の画面テキスト
                                              │  (backend は herdr か rex｡下の「多重化ツールの backend」)
                                              │
                                              │  ここで判定 (下の表)
                                              │
[3]                                           ├─ 上限解除の時刻まで待つ
                                              │  └─ SendText / SendKeys で再開
                                              ├─ context 閾値 → compact-prep → compact → 再開
                                              ├─ cache の失効前 → compact-prep → cache-idle-compacted/<session_id> → compact
                                              └─ 送る直前に cache の失効を見て cache-ack/<session_id> を書く

[4] UserPromptSubmit hook ──stdin──> agents-daemon ttl-guard ── transcript と cache-ack/<session_id> を見て
                                                               失効後の最初の prompt を止める (exit 2)

[5] UserPromptSubmit / Stop hook ──stdin──> agents-daemon herdr-title ── HERDR_SOCKET_PATH の socket へ
                                                                        セッション名とブランチを報告する (herdr だけ)
```

### [1] statusline が上限情報を渡し､バイナリが落とす

Claude Code は statusline コマンドの stdin に JSON を渡す｡その中に
`rate_limits.five_hour.{used_percentage, resets_at}` が入る (`resets_at` は Unix epoch 秒)｡
この値は statusline の stdin にしか来ないので､statusline を経由するのは避けられない｡
配線は 2 通りで､取り出しと書式はどちらもバイナリ (`internal/sessionstate.Store.IngestStatusline`) が持つ
(`agents-daemon:setup` skill の `references/statusline.md`)｡

- `statusLine.command` をバイナリの `statusline` に向ける (推奨)｡描画 (`internal/statusline`) と state file の
  書き込みを同じプロセスで行う｡月額上限の env (`CLAUDE_STATUSLINE_SPEND_CAP`) があれば cost の台帳
  (`internal/spend`) にも記録する
- 利用者の statusline から stdin を `agents-daemon ingest-statusline` へ渡す 1 行を足す｡state file だけを書く

state file の出し分けは次のとおり｡

- `rate_limits` は**初回 API レスポンス後**にだけ現れる｡
- 無いときは何も書かない｡既存ファイルも消さない (まだ届いていないセッションが､他のセッションの
  書いた state を壊さないため)｡
- **5 時間ウィンドウが切り替わる瞬間は `five_hour` が null で届く** (実機で `xx:10:01` の
  ような境界時刻に観測)｡これも書かず､最後に持っていた有効な state を残す｡null で
  上書きすると Go 側は 0 に読むため､待機明けのゲートが「fresh な 0%」で送信を抑制し､
  15 分後に state が古くなって改めて検知しても `resets_at` が無くフォールバック
  (5 時間待ち) に落ちる｡2026-08-28 の取りこぼしはこれが原因だった｡daemon 側も
  `resets_at` が 0 の state は「値が無い」として扱う (`sessionstate.RateLimit.HasFiveHour`)｡
  信号にするのは `resets_at` だけ｡観測されたのは両方 null の形で､`used_percentage`
  だけが null の描画は見ていない｡
- **セッションごとに別ファイルへ書く**｡`resets_at` はアカウント全体で共通だが､
  使用率は観測した時点の値で､セッションによって観測時刻が違う｡1 ファイルを共有すると､
  最後に statusline を描いたセッションの数字で別セッションの pane が判定される｡実際､
  2026-08-19 の取りこぼしでは上限に達したセッションの pane が､無関係なセッション
  (次のウィンドウで動き出した) の使用率 6% でゲート抑制された｡ゲートは自セッションの
  ファイルだけを見る｡他セッションのファイルは起床時刻の算出にだけ使う
  ([rate-limit.md](rate-limit.md) の「待機時刻の決め方」参照)｡
- 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う (`internal/statefile`)｡
- statusline の描画を壊さないよう fail-silent｡失敗しても exit 0 で終わる (理由は stderr)｡
  `ingest-statusline` は stdout に何も出さず､`statusline` は描画だけを出す｡daemon のログにも書かない｡

これがこのツールの中核｡`resets_at` が epoch で直接手に入るので､画面の
`resets 12:30pm (Asia/Tokyo)` のような表記を am/pm やタイムゾーンごとパースする処理が要らない｡
実機で `resets_at = 1786851000` と画面の `12:30pm (Asia/Tokyo)` が一致することを確認済み｡

同じ呼び出しで `context_window.used_percentage` を `context/<session_id>.json` へも書く｡
こちらは `rate_limits` と違って**毎描画で必ず書く** (入力に必ず入っている値であり､
daemon は `observed_at` の新しさでセッションが生きているかも見るため)｡用途は
[compact.md](compact.md)｡

#### statusline が書く state

`$STATE` は下の「出力されるファイル」と同じ `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`｡
どちらも同一ディレクトリの一時ファイルへ書いてから rename で置き換える｡
読み手 (daemon) と書き手 (`statusline` / `ingest-statusline`) は `internal/sessionstate` の同じ型を使うので､
書式はここに転記した参考で､実体はそのパッケージにある｡

`$STATE/rate-limits/<session_id>.json` (`rate_limits.five_hour.resets_at` があるときだけ):

```json
{
  "five_hour": {"used_percentage": 42, "resets_at": 1786851000},
  "seven_day": {"used_percentage": 18, "resets_at": 1787300000},
  "observed_at": 1786840000,
  "session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b"
}
```

`seven_day` は入力にあるときだけ付ける｡`used_percentage` は整数に丸める｡

`$STATE/context/<session_id>.json` (毎描画):

```json
{"session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b", "used_percentage": 63, "observed_at": 1786840000, "context_window_size": 200000, "transcript_path": "/path/to/transcript.jsonl", "multiplexer": {"kind": "rex", "session": "session:01a1…", "block": "block:01a1…"}}
```

`observed_at` はどちらも書いた時点の Unix epoch 秒｡`context_window_size` と `transcript_path` は
入力にあるときだけ付ける (idle compact と cache の状態に使う｡[compact.md](compact.md)､下の節)｡

`multiplexer` は Claude Code が動いている多重化ツールの場所｡statusline のコマンドは Claude Code の環境を
継承するので､バイナリが環境変数から読んで書く (`sessionstate.MultiplexerRefFromEnv`)｡

| 環境変数 | 書く値 |
| ---- | ---- |
| `REX_BLOCK` がある | `{"kind": "rex", "session": $REX_SESSION, "block": $REX_BLOCK}` |
| `HERDR_PANE_ID` がある | `{"kind": "herdr", "pane": $HERDR_PANE_ID}` |
| どちらも無い | フィールドを付けない |

`REX_BLOCK` を先に見るのは､herdr の中で rex を動かすと両方が見え､Claude Code の直接の親は内側の rex だから｡
backend は pane (rex は block) から session_id をこの記録で逆引きする (`Store.ListMultiplexerBlocks`)｡同じ ID の
記録が複数あれば `observed_at` が最新のものを採る (同じ pane で Claude Code を起動し直すと古い記録が残る)｡
`context/` は 7 日で消えるだけなので､5 時間の上限待ちを跨いでも対応は残る｡herdr の pane が持つ
`agent_session` (herdr の integration script が SessionStart で入れる値) は読まない｡herdr と rex の対応付けを
1 本にし､`herdr integration install claude` への依存を切るため｡制約 (別 workspace への `pane move`､初回描画までの
数秒) は [operations.md](operations.md) の「既知の制約」｡

#### daemon が transcript から求める cache の状態

prompt cache は最後のリクエストから TTL (5 分 / 1 時間) で失効し､失効後の 1 送信は文脈全体の
cache write になる｡実際に使われた TTL は transcript JSONL の `message.usage.cache_creation` にしか
無いので､daemon が `context/<session_id>.json` の `transcript_path` を辿り､末尾 2MB を読んで求める
(`internal/sessionstate` の `CacheLoader`)｡読む規則は
[tatsuo48/claude-token-audit の ttl_guard.py](https://github.com/tatsuo48/claude-token-audit/blob/main/plugins/ttl-guard/scripts/ttl_guard.py)
の `last_main_response` と同じ｡求めるのは直近の main の応答の開始時刻 (transcript の timestamp 文字列を
そのまま持つ｡ack と文字列で比較するため)､最新の cache write の TTL､その応答の
`input_tokens + cache_creation_input_tokens + cache_read_input_tokens`｡

Stop hook では読まない｡transcript は非同期に書かれ､Stop の時点ではターンの最後の応答を含まない
ことがある (Claude Code の hooks のドキュメント)｡実機でも､Stop hook が読んだ値はいつも 1 応答前だった｡

daemon が読み直すのは､idle な pane の判定と prompt を送る直前だけ｡transcript の大きさと mtime が
前回と同じなら読み直さない｡working な pane の status には前回読んだ値を出す｡
読む側の使い方 (ack・ログ・status) は [cache.md](cache.md)｡

### [2] SessionStart hook がデーモンを起こす

`hooks/ensure-daemon.sh` が `agents-daemon daemon --ensure` を叩く｡生きているデーモンが既にあれば
即座に戻り､無ければ自分自身を detach 起動して戻る｡hook を絶対にブロックしない｡

**配線は plugin の `hooks/hooks.json` にある｡**

#### バイナリの用意と差し替え

plugin には install 時の lifecycle hook が無いので､バイナリも SessionStart hook が用意する｡

| 状態 | `ensure-daemon.sh` の動き |
| ---- | ---- |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` があり､隣の `agents-daemon.version` が plugin.json の `version` と一致 | `daemon --ensure` を exec する |
| それ以外 (未ビルド､plugin の更新後) | `hooks/build-daemon.sh` を detach して即座に戻る |

`build-daemon.sh` は plugin の root で `go build ./cmd/agents-daemon` を回し､同一ディレクトリの
一時ファイルから `mv` でバイナリを差し替え､stamp を書いてから `daemon --ensure` を叩く｡
出力は `$STATE/logs/build.log` に残る｡SessionStart を `go build` で待たせないため､
初回のセッションでは daemon が build の完了後に起きる｡

- `go` が PATH に無ければ `build.log` に `ERROR ... go not found in PATH` を残して終わる｡
  hook は PATH を仮定しないので､Claude Code を起動するシェルの PATH に Go を入れる｡
- 同時に起きた build は `bin/.build.lock` (ディレクトリ｡中の `pid` が持ち主) で 1 本に絞る｡
  持ち主が生きていれば後から来た側は何もせずに終わる｡build が SIGKILL などで落ちて lock が残っても､
  次の build が持ち主の死を `kill -0` で確かめて残骸を消し､取り直す (`build.log` に `stale lock` が残る)｡
  手で消す必要は無い｡
- plugin の更新後にセッションを開き直さず入れ替えるには､install 済み plugin の `hooks/build-daemon.sh` を
  手で叩く｡コマンドは [operations.md](operations.md) の「plugin の更新後に daemon を入れ替える」｡
- 開発中は `task install` で同じパスへ建てられる｡中身は `hooks/build-daemon.sh` そのもので､
  stamp には clone の plugin.json の version が書かれる｡install 済み plugin と version が同じなら
  次の SessionStart でも建て直されず､clone の版が動き続ける｡plugin 側の版へ戻すときは stamp を消す｡

Go のバイナリは起動時にメモリへ読み込まれるので､ファイルを差し替えても
動作中のプロセスには反映されない｡`--ensure` は「生きているデーモンがあれば何もしない」
ため､放っておくと直したはずのコードが何日も動かないままになる｡

そこでデーモンは毎 tick で自分の実行ファイルの mtime とサイズを見て､差し替わっていたら
後始末をしてから新しい版を detach 起動して入れ替わる (`internal/daemon/selfupdate.go`)｡
build の数秒後には新しい版が動いている｡hook も手動操作も停止・再起動をしない｡

判定できないとき (実行ファイルが一時的に見えない等) は「変わっていない」に倒す｡
動いているデーモンを不用意に落とす方が害が大きいため｡

### [3] デーモンが監視して再開する

単一プロセスが全 Claude pane を見る｡pane ごとの状態は `terminal_id` をキーに
プロセス内で持つ (herdr の `pane_id` は herdr 再起動で変わりうる｡rex は block ID をそのまま使う)｡

### 多重化ツールの backend

daemon は pane 操作を `internal/multiplexer` の `Client` interface 越しに行い､実装は herdr (`internal/herdrcli`)
と rex (`internal/rexcli`) の 2 つ｡`internal/backend` が起動時の環境で選ぶ｡`REX_SESSION` があれば rex､
無ければ herdr｡daemon は SessionStart hook の環境を継承するので､最初に起こしたセッションの側に決まる｡
選んだ backend は `status.json` の `backend` に書き､`doctor` が今の環境と違えば警告する｡

| 操作 | herdr | rex |
| ---- | ---- | ---- |
| PaneList | `pane list` | `ls --json`､session ごとに `block ls -s S --json`､flavor が `com.superlogical.terminal.shell` の block ごとに `block call -b B program_status` と `block call -b B process` |
| PaneRead | `pane read P --source S --lines N` | `capture -b B --trim --unwrap` の末尾 N 行｡`Source` は無視 |
| SendText | `pane send-text P TEXT` | `send -b B TEXT` |
| SendKeys | `pane send-keys P KEY...` | `send-key -b B KEY...` |
| ReportMetadata | `pane report-metadata P --source ... --state-label ...` | 何もしない (`block rename` は利用者の label を潰す) |
| Reachable | `status --json` の `server.running` | `api call server.status` の `pid` が正 |
| session_id | `context/<sid>.json` の `multiplexer.pane` から逆引き (上の「statusline が書く state」) | `context/<sid>.json` の `multiplexer.block` から逆引き (同) |

rex の呼び出しには全部 `--autostart=false` を付け､子プロセスの環境から `REX_SESSION` / `REX_BLOCK` を外す｡
`REX_SESSION` があると rex は block の解決をその session の中に閉じ､外せば完全 ID で別 session の block も
解決する (実測)｡`block ls` と `block call` の間に閉じた block (exit 3) は読み飛ばし､PaneList の失敗に数えない｡

rex の列挙は 1 tick に `ls` 1 回､session ごとに `block ls` 1 回､shell の block ごとに 2 回の fork になる｡
herdr の `pane list` 1 回より重いので､block が多い環境ほど tick あたりの時間が伸びる｡

rex で Agent と AgentStatus を決めるのは Claude Code 自身が OSC 7501 で報告する `program_status` で､
状態の対応は [operations.md](operations.md) の「rex の状態の対応」｡

### [5] herdr の space 欄とタブ行にセッション名を出す

herdr が space 欄に出せる built-in token は pane の cwd から引くもので､worktree へ移ったセッションを追わない｡
端末タイトル (Claude Code がセッションの要約に更新し続ける) は agents 欄にしか出ない｡
`hooks/herdr-title.sh` (UserPromptSubmit / Stop) が stdin を `agents-daemon herdr-title` へ渡し､
バイナリ (`internal/hookreport`) が次を `HERDR_SOCKET_PATH` の socket で herdr へ報告する｡
rex や herdr の外では何もしない (`HERDR_ENV=1` と `HERDR_SOCKET_PATH` と `HERDR_PANE_ID` がそろったときだけ動く)｡

| 面 | 書き込み先 | 値 | hook が無いときの表示 |
| ---- | ---- | ---- | ---- |
| space 欄 | workspace metadata の token `claude` (`workspace.report_metadata`) | `pane.current` の `terminal_title_stripped` | 出す口が無い |
| space 欄 | 同 token `branch` | `foreground_cwd` (git の外なら `cwd`) の `git branch --show-current` | built-in が pane の cwd のブランチを出す |
| タブ行 | タブの label (`tab.rename`) | 端末タイトル｡空ならタブの番号へ戻す | 連番 |

- 描く側は利用者の herdr config の `$claude` と `$branch` の行｡plugin は token を報告するだけ｡
- 書き込み先のタブと workspace は `pane.current` (`caller_pane_id` = `HERDR_PANE_ID`) から取る｡herdr が注入する
  `HERDR_TAB_ID` / `HERDR_WORKSPACE_ID` は pane 作成時の値のままで､`herdr pane move` の後は元のタブを指す｡
- 空の token は null で送って消す｡`tab.rename` に空を渡すと空の label になるので､番号を渡す｡
- source は `claude-config:claude-title`｡agents-config の `herdr-claude-title.sh` から移したもので､移行期間は
  両方の hook が同じ source に報告し､seq (UnixNano) の新しい方が勝つ｡
- 値を取り直すのは hook の発火時点だけ｡agents 欄は端末タイトルを直に読むので､長いターンの間は space 欄とずれる｡
- 失敗は exit 0 で理由を stderr に出す (hook のログ)｡herdr との往復と git を合わせて 4 秒で諦める｡

## 毎 tick の判定

pane ごとに次を評価する｡上限側の詳細は [rate-limit.md](rate-limit.md)､compact 側は
[compact.md](compact.md)､画面の読み取りと送信の作法は [pane-io.md](pane-io.md)｡

| 条件 | 動作 |
| ---- | ---- |
| 待機中で､まだ待機期限に達していない | 何もしない |
| 上記以外 | 画面を読んで分類する (`agent_status` は見ない) |
| ネイティブの auto-continue が再開を予約している | 手を引く (待機中なら監視へ戻す) |
| 上限が解除済みで Enter 待ち | 通常の待機に入り､送信では Enter だけを送る |
| 待っても解除されない上限 (spend limit / usage credit) | 待機に入らない |
| 画面が limit と判定され､ゲートを通過した | 待機に入る |
| 待機中に limit が消えた | 監視に戻る (ユーザーが自分で再開した｡プロンプトは送らない) |
| 待機期限に達した | 再開プロンプトを送り､試行回数を増やす |
| 試行回数が `maxRetries` に達した | 長いクールダウンに入る |
| 一時的なサーバーエラー (5xx / 過負荷) | 指数バックオフで短周期リトライ |
| どれにも該当せず､context 使用率が閾値を超えている | `/agents-daemon:compact-prep` を投入する (別ゲート) |
| どれにも該当せず､cache の失効が近く context 使用率が idle compact の閾値を超えている | `/agents-daemon:compact-prep` を投入する (別ゲート｡idle compact) |
| どれにも該当せず､compact-prep の state file が投入後に書かれた | `/compact` を投入する (別ゲート｡idle compact なら先に marker を書く) |
| どれにも該当せず､圧縮完了 marker が置かれている | 作業の再開を促すメッセージを送る (別ゲート｡idle compact の marker が active なら送らない) |
| どれにも該当せず､compact 直後で止まっている (画面判定) | 継続を促すメッセージを送る (別ゲート｡同上) |

## 出力されるファイル

設定は利用者が書くものなので XDG の config ディレクトリに置く｡ランタイム状態は揮発物なので
ハーネスの `~/.claude/` に混ぜず､XDG Base Directory の state ディレクトリ
`${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` (以下 `$STATE`) に置く｡
`XDG_STATE_HOME` は daemon と `statusline` / `ingest-statusline` / `ttl-guard` (`apppath.FromEnv`)・hook
(`hooks/lib/compact-markers.sh`) が同じ規則で解決する｡

| パス | 書く主体 | いつ | 中身 | 消す主体 |
| ---- | ---- | ---- | ---- | ---- |
| `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` | 人間 | 手で編集したとき | 設定 | 消さない |
| `$STATE/rate-limits/<session_id>.json` | `agents-daemon statusline` または `ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと (`rate_limits.five_hour.resets_at` があるときだけ) | そのセッションの 5 時間 / 7 日ウィンドウの使用率と reset 時刻､観測時刻､session_id | daemon が 24 時間で消す |
| `$STATE/context/<session_id>.json` | `agents-daemon statusline` または `ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと | そのセッションの context 使用率､観測時刻､session_id､context window の大きさ､transcript のパス､多重化ツールの場所 | daemon が 7 日で消す |
| `$STATE/cost/<YYYY-MM-DD>/<session_id>.json` | `agents-daemon statusline` (`CLAUDE_STATUSLINE_SPEND_CAP` があるとき) | statusline の描画ごと (累計が変わったときだけ書き直す) | そのセッションの累計 cost と前日までの繰り越し (`internal/spend`)｡daemon は読まない | `statusline` がその日の最初の書き込みで 45 日より古い日を消す |
| `$STATE/compact-state/<session_id>.md` | `agents-daemon:compact-prep` skill | `/agents-daemon:compact-prep` の実行時 | 圧縮で失われる作業状態 (plan / phase / 決定事項 / 編集中ファイル) | daemon が 7 日で消す |
| `$STATE/compacted/<session_id>` | `hooks/compaction-recovery.sh` (PostCompact hook) | 圧縮が完了したとき | 空ファイル｡mtime が圧縮完了時刻 | 復旧 hook が消す (残れば daemon が 7 日で消す) |
| `$STATE/cache/<session_id>.json` | 旧版の Stop hook (今は書かない) | - | 旧版の prompt cache の状態 | daemon が 7 日で消す |
| `$STATE/cache-ack/<session_id>` | daemon と `agents-daemon ttl-guard` | daemon は非スラッシュの prompt を送る直前 (cache が失効しているか状態が分からないとき)､ttl-guard は失効後の prompt を判定したとき | 直近の応答の開始時刻の文字列そのまま､または sentinel `daemon-unknown` ([cache.md](cache.md)) | daemon と ttl-guard が 90 日で消す |
| `$STATE/cache-idle-compacted/<session_id>` | daemon | idle compact の `/compact` を送る直前 | 空ファイル｡mtime が送信時刻 ([compact.md](compact.md)) | 消さずに直近の応答の時刻との比較で解く (daemon が 7 日で消す) |
| `$STATE/daemon.pid` | daemon | 起動時に作成､終了時に削除 | 稼働中デーモンの PID | daemon |
| `$STATE/status.json` | daemon | 毎 tick (既定 5 秒) | backend (herdr / rex)､監視中の pane 一覧と各 pane の監視状態・試行回数・待機期限・直近の判定・cache の状態 | 上書き |
| `$STATE/logs/daemon.log` | daemon | 報告に値する出来事があったときだけ | 上限検知と待ち時間､再開送信 (cache の状態つき)､ユーザーの自己再開､ゲート抑制､ネイティブへの譲り､compact 停止の催促､エラー | rotate |
| `$STATE/logs/daemon.log.YYYY-MM-DD` | daemon | 日付が変わって最初に書くとき | 前日までの `daemon.log` をそのまま退避したもの | daemon が 7 日で消す |
| `$STATE/logs/build.log` | `hooks/build-daemon.sh` | SessionStart が build を起こしたとき | build の開始・完了・失敗 | 消さない (追記) |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` | `hooks/build-daemon.sh` / `task install` | build 時 | 実行ファイル | 消さない |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon.version` | `hooks/build-daemon.sh` | build の成功時 | 建てた元の plugin version | 消さない |

`$STATE` はデーモンが必要に応じて作る｡消しても壊れない
(次回の起動と次回の statusline 描画で再生成される)｡

静かな tick は**何も書かない**｡ログが伸びていないことは､何も起きていないことを意味する｡

### ログの rotate と自動削除

現行ログの名前は `daemon.log` で固定する｡`tail -f` や `logs` サブコマンドの参照先が
日付で変わらないようにするため｡日付が変わって最初に書くとき､それまでの `daemon.log` を
最終書き込み日の名前 (`daemon.log.YYYY-MM-DD`) へ rename してから新しいファイルへ書く｡
daemon の再起動を跨いで日付が変わっていた場合も､mtime から最終書き込み日を取って同じように
退避する｡退避先が既にあるとき (時計の巻き戻り等) は上書きせず､次の日付境界まで
`daemon.log` へ追記し続ける｡

古いファイルは daemon 自身が消す｡起動時と 1 時間ごとに､mtime が保持期間を過ぎた
rotate 済みログ (7 日)､`rate-limits/` 配下の state (24 時間｡ingest-statusline が rename 前に
死んで残った `.rate-limits.*` も同じ扱い)､compact 関連の 3 ディレクトリと `cache/`・`cache-idle-compacted/` 配下 (7 日)､
`cache-ack/` 配下 (90 日) を削除する｡何かを消したときだけログに残す｡

保持期間は設定キーにせず定数 (`internal/daemon/housekeeping.go`) に置く｡state の
24 時間は判定に影響しない値として選んである: `resets_at` は観測から高々 5 時間先で､
起床時刻の算出 (`LatestWindow`) は未来の `resets_at` しか使わない｡compact 側を 7 日と
長く取るのは､復旧用 state file が「次のプロンプトで注入される」まで待つ側であり､
セッションが放置されている間に消すと復旧材料そのものを失うため｡
