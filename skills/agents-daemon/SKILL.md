---
name: agents-daemon
description: >-
  agents-daemon plugin (利用上限からの自動再開と context の自動 compact を担う常駐デーモン) の運用と切り分けに使う｡
  上限が解除されても再開が飛ばない､context が閾値を超えても compact が投入されない､圧縮後に作業が再開しない､
  関係ない pane を誤検知して打ち込む､prompt が cache 失効の警告で止められる､daemon が起きない､
  バイナリがビルドされない・直したコードが動かない､
  といった症状から引く｡statusline / hook / skill / daemon のファイル越しの配線と､設定キーの一覧も持つ｡
---

# agents-daemon

herdr のペインを経由して動く常駐デーモン｡役目は 2 つ｡

- 5 時間ウィンドウの利用上限で止まったセッションを､解除時刻に再開させる
- context 使用率が閾値を超えたら `/agents-daemon:compact-prep` → `/compact` を投入し､圧縮後の作業まで再開させる
  (離席中の pane を prompt cache の失効前に compact する idle compact も同じ 2 段を使う)

前者はネイティブ機能 `autoContinueAtUsageLimit` (既定 on) を置き換えるものではなく､
ネイティブが降りた場面を拾う側に回る｡

## 配線の要点

- 部品はファイル越しに繋がり､互いを直接呼ばない｡利用者の statusline から呼ばれた
  `agents-daemon ingest-statusline` が `rate-limits/<session_id>.json` と `context/<session_id>.json` を書き､
  `agents-daemon:compact-prep` skill が `compact-state/<session_id>.md` を書き､PostCompact hook が
  `compacted/<session_id>` を書き､Stop hook から呼ばれた `agents-daemon ingest-stop` が
  `cache/<session_id>.json` を書く｡UserPromptSubmit hook から呼ばれた `agents-daemon ttl-guard` は
  cache 失効後の最初の prompt を止め､daemon が送る前に書く `cache-ack/<session_id>` を見て daemon の
  送信は通す｡SessionStart hook が daemon を起こし､
  daemon が herdr 経由で pane を見て､これらのファイルの有無と mtime で判断する｡
- **statusline はこの plugin に含まれない｡** 利用者の statusline がするのは stdin を
  `ingest-statusline` へ渡す 1 行だけで､取り出しと書式はバイナリ (`internal/sessionstate`) が持つ｡
  書式は [architecture.md](references/architecture.md) の「ingest-statusline が書く state」にある｡
  1 行が無ければ上限側のゲートと compact の自動投入が働かない｡
- ingest-statusline が書き出すのは Claude Code が statusline の stdin で渡す `rate_limits.five_hour` と
  `context_window.used_percentage`｡`resets_at` は Unix epoch 秒で､これがあるので
  画面の時刻表記をパースしなくて済む｡使用率は statusline の stdin にしか来ないため､
  compact の判定はこの経路が唯一の情報源｡
- compact 側の判定に画面の文字列を使わない｡実機の compact 直後の画面には圧縮の境界行が
  残らず､画面判定 (`internal/detect/compact.go`) は候補を作れない｡代わりに
  PostCompact hook が置く marker を見る｡この経路はユーザーが手で打った `/compact` でも
  本体の autocompact でも動く｡
- hook の配線元は plugin の `hooks/hooks.json` (SessionStart / PostCompact / UserPromptSubmit / Stop)｡
- marker と state file の置き場は `hooks/lib/compact-markers.sh` の定数 (hook /
  skill 側) と `internal/apppath` (daemon 側) が対で持つ｡片方だけ変えると protocol が
  黙って壊れる｡cwd 相対にしないのは､daemon が別プロセスであり hook に渡る cwd も
  `cd` や worktree で動くため｡
- session_id の解決は `CLAUDE_CODE_SESSION_ID` を唯一の情報源にする
  (`scripts/get-session-id.sh`)｡プロジェクトの JSONL の mtime から推測すると､
  同一プロジェクトで複数セッションが動いているときに他セッションの state file を上書きする｡
- 設定は `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`｡
  ランタイム状態 (`daemon.pid` / `status.json` / `rate-limits/` / `context/` /
  `compact-state/` / `compacted/` / `cache/` / `cache-ack/` / `cache-idle-compacted/` / `logs/`) は
  `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` に置く｡
  ログの rotate と古いファイルの削除は daemon 自身が行う｡
- バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` に置く｡plugin の
  install 先はバージョンごとに変わるため､その外の固定パスにする｡用意するのは SessionStart hook で､
  詳細は [architecture.md](references/architecture.md) の「バイナリの用意と差し替え」｡
- `compactAutoEnabled` は Go の既定を `false` にしてある｡`/compact` は取り消せないため､
  コードの既定は投入しない側へ倒す｡使うなら config.json で `true` にする｡
  `cacheIdleCompactEnabled` (idle compact) は既定 `true` (理由は operations.md の「送る文面」の後段)｡
- sandbox 内のセッションから `stop` を実行すると SIGTERM が
  `operation not permitted` で弾かれる｡デーモン側の後始末は正常なので､
  通常のターミナルから実行するか自己終了に任せる｡

## references

`Read` で開く｡

| ファイル | 中身 |
| ---- | ---- |
| [architecture.md](references/architecture.md) | statusline / hook / skill / daemon の連動､バイナリの用意と差し替え､毎 tick の判定表､出力されるファイル､ログの rotate |
| [rate-limit.md](references/rate-limit.md) | ネイティブ auto-continue との分担､limit 検知の二重シグナルとゲート､待機時刻の決め方 |
| [compact.md](references/compact.md) | context 閾値からの 3 段､marker 経路､cache の失効前の compact (idle compact)､compact 直後の停止 |
| [cache.md](references/cache.md) | prompt cache の失効と daemon の送信､ack マーカーの規約､対象の prompt 4 つ､送信ログと status の cache 表示 |
| [pane-io.md](references/pane-io.md) | 画面の読み取り元､送信のキーストロークと文面の制約 |
| [operations.md](references/operations.md) | コマンドと `--dry-run`､実機での検証順序､設定キー一覧､既知の制約 |

## 症状から引くところ

| 症状 | 見るところ |
| ---- | ---- |
| install したばかり､前提がそろっているか分からない | `agents-daemon:setup` skill |
| 上限が解除されても再開しない | rate-limit.md の「待機時刻の決め方」「ゲート」 |
| 関係ない pane にプロンプトが打ち込まれた | rate-limit.md の「limit の判定は 2 つの手がかりを要求する」「ゲート」 |
| context が閾値を超えても compact が走らない | compact.md の「送信の条件」､operations.md の「既知の制約」 |
| 圧縮後に作業が再開しない | compact.md の「なぜ画面を読まないか」「compact 直後の停止」｡離席中の圧縮なら「idle compact」(再開しないのが仕様) |
| 再開の送信が高くついた・どれだけ書き直したか知りたい | cache.md の「ログ」(送信行末の `cache=expired` と context 使用率) |
| prompt が止められて「prompt cache は N 分で失効した」と警告が出る | cache.md の「TTL guard」 |
| daemon の再開が止められる・`compactStallMessage` が繰り返し打たれる | cache.md の「TTL guard」の ack の規則､ログの `ack 済み` / `session 未紐づけ` |
| daemon が起きない・バイナリがビルドされない | architecture.md の「バイナリの用意と差し替え」､`build.log` |
| 直したコードが動いていない | architecture.md の「バイナリの用意と差し替え」 |
| 送信が pane に届かない | pane-io.md､operations.md の「既知の制約」(pane 内 tmux､herdr 不在) |
