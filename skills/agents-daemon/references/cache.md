# prompt cache と daemon の送信

sidecar `cache/<session_id>.json` を書く側 (Stop hook → `ingest-stop`) は
[architecture.md](architecture.md) の「ingest-stop が書く state」にある｡ここは読む側､つまり daemon が
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
| `compactAutoPrepMessage` | 1 段目 (`/agents-daemon:compact-prep`) | する | 書かない (ログのみ) |
| `compactAutoMessage` | 2 段目 (`/compact ...`) | する | 書かない (ログのみ) |

スラッシュかどうかは **送る文面そのもの** (`TrimSpace` して `/` 始まり) で判定する｡どの文面も設定で
書き換えられるので､設定キーで決め打ちしない｡TTL guard hook はスラッシュコマンドを素通しするので
ack は要らない｡

## ack マーカー

`$STATE/cache-ack/<session_id>` (拡張子なし)｡daemon が非スラッシュの prompt を送る **直前** に書き､
TTL guard hook (`UserPromptSubmit`) がこれを見て daemon の送信を止めずに通す｡「書き直しのコストを
承知で送る」の意思表示｡置き場と sentinel は `hooks/lib/compact-markers.sh` (`CACHE_ACK_DIR` /
`cache_ack_file` / `CACHE_ACK_UNKNOWN`) と `internal/sessionstate` (`Store.CacheAck` / `WriteCacheAck` /
`CacheAckUnknown`) が対で持つ｡

| sidecar の状態 | 書く値 |
| ---- | ---- |
| ある､失効 (`now - last_request_at >= ttl`) | `last_request_at` の文字列そのまま |
| ある､warm | 書かない (guard も止めない) |
| 無い (Stop hook 未導入､初回ターン) | sentinel `daemon-unknown` |

- 値は sidecar の文字列を **整形せずに写す**｡`time.Parse` / `Format` を通すとミリ秒が落ちたり `Z` が
  `+00:00` になったりして､guard の文字列比較 (完全一致) と合わなくなる｡末尾の改行も入れない｡
- sidecar が無くても **必ず何かを書く**｡書かないと guard が daemon の再開を止め､daemon は pane の停止と
  見て `compactStallMessage` を打ち､それも止められて堂々巡りになる｡guard は sentinel の mtime が
  直近数十秒以内なら通し､transcript から求めた実際の値で書き換える｡同じ値でも書き直して mtime を
  新しくする｡
- `last_request_at` が未来 (clock skew) なら warm 扱い｡
- ack の書き込みに失敗しても送信は止めない｡失敗はログに残る｡
- `--dry-run` では書かない｡書くと利用者自身の次の prompt が guard を素通りする｡
- herdr が pane にセッションを紐づけていなければ書けない (ログに `session 未紐づけ` と出る)｡
- daemon が 90 日で消す｡ack は時間で失効させない (待った時間が長くなっても書き直しのコストは同じ)
  ので compact 系の 7 日より長い｡

Stop hook はユーザーの中断では発火しないので､sidecar が transcript より古いことがある｡daemon は
sidecar の値を写すため､guard が transcript から求めた値と食い違いうる｡どちらも失効側にずれる
(「まだ warm」を「失効」と見る) ので送る側の判断は変わらないが､ack の文字列は一致しない｡
この扱いは guard 側の規則 (mtime が直近の ack を通す) に寄せる｡

## ログ

prompt を送った出来事 (再開送信､compact-prep / compact の投入､compact 後の再開) の行末に cache の
状態を添える｡

```text
pane 3 (/path): 再開メッセージを送信しました (1 回目) / cache=expired (経過 312 分､TTL 60 分､context 使用率 63%､ack 済み)
pane 3 (/path): compact 直後に止まっていたため継続を促しました (画面判定) / cache=warm (経過 3 分､TTL 60 分)
pane 3 (/path): 再開メッセージを送信しました (1 回目) / cache=unknown (sidecar 無し､ack=daemon-unknown)
pane 3 (/path): context 使用率が閾値を超えたため compact-prep を投入しました / cache=expired (経過 90 分､TTL 60 分､context 使用率 76%､スラッシュなので ack なし)
```

失効後の送信は `context/<session_id>.json` の使用率を併記する｡書き直したのは文脈全体なので､
その時点の使用率がそのまま書き直しの量になる｡

## status

`status.json` の pane ごとに `cache` を持つ (sidecar が無ければ省略)｡`state` は tick 時点の判定で､
失効までの残りは読む側が `expires_at` から出す｡

```json
{"state": "warm", "last_request_at": "2026-09-27T03:44:05.954Z", "ttl_seconds": 3600, "expires_at": "2026-09-27T04:44:05.954Z"}
```

`agents-daemon status` の pane 行は `cache=warm(残り 42 分)` / `cache=expired(失効から 258 分)` / `cache=-`｡
残りと経過は表示時点で計算する｡
