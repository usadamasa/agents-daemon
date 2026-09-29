# prompt cache と daemon の送信

cache の状態を transcript から求める仕組みは [architecture.md](architecture.md) の
「daemon が transcript から求める cache の状態」にある｡ここは使う側､つまり daemon が
prompt を送るときに cache の状態をどう扱うか｡

## なぜ見るか

prompt cache は最後のリクエストから TTL (5 分 / 1 時間) で失効し､失効後の 1 送信は文脈全体を cache write
として送り直す ([prompt-caching](https://code.claude.com/docs/en/prompt-caching.md))｡daemon 自身が打ち込む
prompt もこの対象で､特に利用上限からの再開は 5 時間待った後なので必ず失効している｡

このコストは避けられない｡再開しなければ意味が無く､失効後の `/compact` も全履歴を非 cache で送り直す
(上限中は `/compact` も API 呼び出しなので通らない)｡だから **送る・送らないの判断は変えない**｡
daemon がするのは､失効を知った上で送ること (ack)､どれだけ書き直したかを残すこと (ログ)､
今どうなっているかを見せること (status) の 3 つ｡失効を理由に再開を止める設定は無い
(それは `enabled: false` と同じ)｡

## 対象の prompt

daemon が pane へ打ち込む文面は 4 つ (`internal/config` の既定値､いずれも config.json で書き換えられる)｡

| 設定キー | 経路 | スラッシュ | ack |
| ---- | ---- | ---- | ---- |
| `retryMessage` | 上限解除後の再開 (`recoverPane`) | しない | 失効後なら書く |
| `compactStallMessage` | compact 後の再開 (marker 経路と画面判定の両方) | しない | 失効後なら書く |
| `compactAutoPrepMessage` | 1 段目 (`/agents-daemon:compact-prep`)｡idle compact も同じ | する | 書かない (ログのみ) |
| `compactAutoMessage` | 2 段目 (`/compact ...`)｡idle compact も同じ | する | 書かない (ログのみ) |

スラッシュかどうかは **送る文面そのもの** (`TrimSpace` して `/` 始まり) で判定する｡どの文面も設定で
書き換えられるので､設定キーで決め打ちしない｡TTL guard hook はスラッシュコマンドを素通しするので
ack は要らない｡

## ack マーカー

`$STATE/cache-ack/<session_id>` (拡張子なし)｡daemon が非スラッシュの prompt を送る **直前** に書き､
TTL guard (下の節) がこれを見て daemon の送信を止めずに通す｡「書き直しのコストを承知で送る」の
意思表示｡書く側も読む側も `internal/sessionstate` (`WriteCacheAck` / `CacheAckUnknown` / `TTLGuard`) にある｡

| cache の状態 | 書く値 |
| ---- | ---- |
| ある､失効 (`now - last_request_at >= ttl`) | 直近の応答の開始時刻 (transcript の文字列そのまま) |
| ある､warm | 書かない (guard も止めない) |
| 無い (statusline が `transcript_path` を渡していない､初回ターン) | sentinel `daemon-unknown` |

- 値は transcript の文字列を **整形せずに写す**｡`time.Parse` / `Format` を通すとミリ秒が落ちたり `Z` が
  `+00:00` になったりして､guard の文字列比較 (完全一致) と合わなくなる｡末尾の改行も入れない｡
- 状態が無くても **必ず何かを書く**｡書かないと guard が daemon の再開を止め､daemon は pane の停止と
  見て `compactStallMessage` を打ち､それも止められて堂々巡りになる｡guard は sentinel の mtime が
  直近数十秒以内なら通し､transcript から求めた実際の値で書き換える｡同じ値でも書き直して mtime を
  新しくする｡
- `last_request_at` が未来 (clock skew) なら warm 扱い｡
- ack の書き込みに失敗しても送信は止めない｡失敗はログに残る｡
- `--dry-run` では書かない｡書くと利用者自身の次の prompt が guard を素通りする｡ログには
  `ack は dry-run で省略` と出る｡
- herdr が pane にセッションを紐づけていなければ書けない (ログに `session 未紐づけ` と出る)｡
- daemon が 90 日で消す｡ack は時間で失効させない (待った時間が長くなっても書き直しのコストは同じ)
  ので compact 系の 7 日より長い｡

daemon と guard は同じ transcript を同じ規則で読むので､ack の文字列はふつう一致する｡daemon が読んだ後に
transcript の書き込みが追いつくと食い違うが､guard は値が違っても mtime が直近の ack を daemon の送信として
通す (下の節)｡

## TTL guard

UserPromptSubmit hook `hooks/ttl-guard.sh` が stdin を `agents-daemon ttl-guard` へ渡す｡
cache 失効後の最初の prompt を 1 回だけ止め (exit 2)､経過時間と選択肢 (`/clear`､同じ prompt の再送､
`/compact` してから続ける) を警告として見せる｡TTL が 5 分なら `promptCacheTtl: "1h"` も案内する｡
参考実装は [claude-token-audit の ttl-guard](https://github.com/tatsuo48/claude-token-audit#prevent-r1-with-ttl-guard)｡

判定は transcript を直接読む｡読み方は daemon と同じ `lastMainResponse`｡

| 状況 | 結果 |
| ---- | ---- |
| prompt がスラッシュで始まる (`/clear` `/compact` など) | 通す |
| transcript が無い､cache write が無い､compact 後にまだ応答が無い､warm | 通す |
| 失効､ack の中身が `last_request_at` と同じ | 通す (同じ idle gap で警告済みか､daemon が承知で送った) |
| 失効､ack の mtime が直近 60 秒以内 (中身は問わない) | 通して実際の値で書き換える (daemon の送信) |
| 失効､上のどれでもない | ack を書き換えて止める |

- 中身を問わず mtime で通すのは､sentinel `daemon-unknown` と､daemon が読んだ後に transcript が伸びた
  ときの値の両方を拾うため｡値の一致は「同じ gap で既に警告した」の判定にだけ使う
- compact_boundary より前の応答は見ない｡compact 後の次のリクエストは要約だけを送るので､手で `/compact`
  した後の最初の prompt を止めない (止めると並列の復旧 hook が marker を消し､復旧ガイドが失われる)｡
  daemon も同じ規則で､compact 後に応答が無ければ cache の状態を無しとする
- ack は時間で失効させない｡90 日より古い ack は guard も消す
- バイナリが無い・失敗した (サブコマンドを持たない古いバイナリも含む) ときは止めない｡理由は stderr と hook のログ

## ログ

prompt を送った出来事 (再開送信､compact-prep / compact の投入､compact 後の再開) の行末に cache の
状態を添える｡

```text
pane 3 (/path): 再開メッセージを送信しました (1 回目) / cache=expired (経過 312 分､TTL 60 分､context 使用率 63%､ack 済み)
pane 3 (/path): compact 直後に止まっていたため継続を促しました (画面判定) / cache=warm (経過 3 分､TTL 60 分)
pane 3 (/path): 再開メッセージを送信しました (1 回目) / cache=unknown (状態無し､ack=daemon-unknown)
pane 3 (/path): context 使用率が閾値を超えたため compact-prep を投入しました / cache=expired (経過 90 分､TTL 60 分､context 使用率 76%､スラッシュなので ack なし)
```

失効後の送信は `context/<session_id>.json` の使用率を併記する｡書き直したのは文脈全体なので､
その時点の使用率がそのまま書き直しの量になる｡

## status

`status.json` の pane ごとに `cache` を持つ (状態が無ければ省略)｡`state` は tick 時点の判定で､
失効までの残りは読む側が `expires_at` から出す｡working な pane では transcript を読み直さず､前回読んだ
値を出す (応答のたびに伸びるため)｡daemon が起きてからまだ一度も idle にならない pane では省略される｡

```json
{"state": "warm", "last_request_at": "2026-09-27T03:44:05.954Z", "ttl_seconds": 3600, "expires_at": "2026-09-27T04:44:05.954Z"}
```

`agents-daemon status` の pane 行は `cache=warm(残り 42 分)` / `cache=expired(失効から 258 分)` / `cache=-`｡
残りと経過は表示時点で計算する｡
