# agents-daemon の全体構成と出力ファイル

## 全体の連動

statusline・hook・skill・daemon が別々のタイミングで動き､ファイルを介して繋がる｡
互いを直接呼ばない｡

```
[1] Claude Code ──毎描画── statusline ──stdin──> agents-daemon ingest-statusline
                     (利用者側)                    ├──> rate-limits/<session_id>.json (あれば)
                                                   └──> context/<session_id>.json     (毎回)
    compact-prep skill ────────────────────> compact-state/<session_id>.md
    PostCompact hook ──────────────────────> compacted/<session_id>
    Claude Code ──非同期に追記──> transcript (context/ の transcript_path が指す)
                                                                          │
[2] SessionStart hook ──daemon --ensure──> daemon (常駐 1 プロセス)        │ pane の
                                              │                           │ session を
                                              │ 毎 tick (既定 5 秒) <──────┘ 引いて読む
                                              │
                                              ├─ herdr pane list ─────> Claude の pane 一覧
                                              ├─ herdr pane read ─────> 各 pane の画面テキスト
                                              │
                                              │  ここで判定 (下の表)
                                              │
[3]                                           ├─ 上限解除の時刻まで待つ
                                              │  └─ herdr pane send-text / send-keys で再開
                                              ├─ context 閾値 → compact-prep → compact → 再開
                                              ├─ cache の失効前 → compact-prep → cache-idle-compacted/<session_id> → compact
                                              └─ 送る直前に cache の失効を見て cache-ack/<session_id> を書く

[4] UserPromptSubmit hook ──stdin──> agents-daemon ttl-guard ── transcript と cache-ack/<session_id> を見て
                                                               失効後の最初の prompt を止める (exit 2)
```

### [1] statusline が上限情報を渡し､ingest-statusline が落とす

Claude Code は statusline コマンドの stdin に JSON を渡す｡その中に
`rate_limits.five_hour.{used_percentage, resets_at}` が入る (`resets_at` は Unix epoch 秒)｡
この値は statusline の stdin にしか来ないので､statusline を経由するのは避けられない｡
ただし statusline がするのは stdin を `agents-daemon ingest-statusline` へ渡すことだけで､
取り出しと書式はバイナリ (`internal/sessionstate.Store.IngestStatusline`) が持つ｡
statusline はこの plugin に含まれないので､利用者の statusline に呼び出しの 1 行を足す
(`agents-daemon:setup` skill の `references/statusline.md`)｡

ingest-statusline の出し分けは次のとおり｡

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
- 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う｡
- statusline の描画を壊さないよう fail-silent｡stdout には何も出さず､失敗しても exit 0 で終わる
  (理由は stderr に出すが､呼び出しの 1 行が `2>/dev/null` で捨てる)｡daemon のログにも書かない｡

これがこのツールの中核｡`resets_at` が epoch で直接手に入るので､画面の
`resets 12:30pm (Asia/Tokyo)` のような表記を am/pm やタイムゾーンごとパースする処理が要らない｡
実機で `resets_at = 1786851000` と画面の `12:30pm (Asia/Tokyo)` が一致することを確認済み｡

同じ呼び出しで `context_window.used_percentage` を `context/<session_id>.json` へも書く｡
こちらは `rate_limits` と違って**毎描画で必ず書く** (入力に必ず入っている値であり､
daemon は `observed_at` の新しさでセッションが生きているかも見るため)｡用途は
[compact.md](compact.md)｡

#### ingest-statusline が書く state

`$STATE` は下の「出力されるファイル」と同じ `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`｡
どちらも同一ディレクトリの一時ファイルへ書いてから rename で置き換える｡
読み手 (daemon) と書き手 (ingest-statusline) は `internal/sessionstate` の同じ型を使うので､
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
{"session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b", "used_percentage": 63, "observed_at": 1786840000, "context_window_size": 200000, "transcript_path": "/path/to/transcript.jsonl"}
```

`observed_at` はどちらも書いた時点の Unix epoch 秒｡`context_window_size` と `transcript_path` は
入力にあるときだけ付ける (idle compact と cache の状態に使う｡[compact.md](compact.md)､下の節)｡

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
プロセス内で持つ (`pane_id` は herdr 再起動で変わりうる)｡

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
`XDG_STATE_HOME` は daemon と `ingest-statusline` / `ttl-guard` (`apppath.FromEnv`)・hook
(`hooks/lib/compact-markers.sh`) が同じ規則で解決する｡

| パス | 書く主体 | いつ | 中身 | 消す主体 |
| ---- | ---- | ---- | ---- | ---- |
| `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` | 人間 | 手で編集したとき | 設定 | 消さない |
| `$STATE/rate-limits/<session_id>.json` | `agents-daemon ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと (`rate_limits.five_hour.resets_at` があるときだけ) | そのセッションの 5 時間 / 7 日ウィンドウの使用率と reset 時刻､観測時刻､session_id | daemon が 24 時間で消す |
| `$STATE/context/<session_id>.json` | `agents-daemon ingest-statusline` (利用者の statusline が呼ぶ) | statusline の描画ごと | そのセッションの context 使用率､観測時刻､session_id､context window の大きさ､transcript のパス | daemon が 7 日で消す |
| `$STATE/compact-state/<session_id>.md` | `agents-daemon:compact-prep` skill | `/agents-daemon:compact-prep` の実行時 | 圧縮で失われる作業状態 (plan / phase / 決定事項 / 編集中ファイル) | daemon が 7 日で消す |
| `$STATE/compacted/<session_id>` | `hooks/compaction-recovery.sh` (PostCompact hook) | 圧縮が完了したとき | 空ファイル｡mtime が圧縮完了時刻 | 復旧 hook が消す (残れば daemon が 7 日で消す) |
| `$STATE/cache/<session_id>.json` | 旧版の Stop hook (今は書かない) | - | 旧版の prompt cache の状態 | daemon が 7 日で消す |
| `$STATE/cache-ack/<session_id>` | daemon と `agents-daemon ttl-guard` | daemon は非スラッシュの prompt を送る直前 (cache が失効しているか状態が分からないとき)､ttl-guard は失効後の prompt を判定したとき | 直近の応答の開始時刻の文字列そのまま､または sentinel `daemon-unknown` ([cache.md](cache.md)) | daemon と ttl-guard が 90 日で消す |
| `$STATE/cache-idle-compacted/<session_id>` | daemon | idle compact の `/compact` を送る直前 | 空ファイル｡mtime が送信時刻 ([compact.md](compact.md)) | 消さずに直近の応答の時刻との比較で解く (daemon が 7 日で消す) |
| `$STATE/daemon.pid` | daemon | 起動時に作成､終了時に削除 | 稼働中デーモンの PID | daemon |
| `$STATE/status.json` | daemon | 毎 tick (既定 5 秒) | 監視中の pane 一覧と各 pane の監視状態・試行回数・待機期限・直近の判定・cache の状態 | 上書き |
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
