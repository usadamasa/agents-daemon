---
name: agents-daemon
description: >-
  agents-daemon plugin (利用上限からの自動再開を担う常駐デーモン) の運用と切り分けに使う｡
  上限が解除されても再開が飛ばない､関係ない pane を誤検知して打ち込む､daemon が起きない､
  バイナリがビルドされない・直したコードが動かない､statusline が state file を書かない､
  といった症状から引く｡statusline / hook / daemon のファイル越しの配線と､設定キーの一覧も持つ｡
---

# agents-daemon

端末多重化ツール rex の block を経由して動く常駐デーモン｡役目は 1 つ｡

- 5 時間ウィンドウの利用上限で止まったセッションを､解除時刻に再開させる

ネイティブ機能 `autoContinueAtUsageLimit` (既定 on) を置き換えるものではなく､
ネイティブが降りた場面を拾う側に回る｡

context の compact と prompt cache の失効には関わらない｡Claude Code 本体の autocompact・idle compaction
(2.1.299) と失効後の footer の案内に任せる｡以前この plugin が持っていた compact の自動化 (compact-prep →
`/compact` → 再開) と ttl-guard は #59 で外した｡本体の idle compaction の後に daemon が離席中の pane へ
再開を打ち込む衝突が起きていたため｡

## 配線の要点

- 部品はファイル越しに繋がり､互いを直接呼ばない｡statusline として呼ばれた
  `agents-daemon statusline` (または利用者の statusline から呼ばれた `ingest-statusline`) が
  `rate-limits/<session_id>.json` と `context/<session_id>.json` を書き､SessionStart hook が daemon を起こし､
  daemon が rex 経由で block を見て､これらのファイルで判断する｡
  rex の外では daemon を起こさない｡操作の対応は [architecture.md](references/architecture.md) の「多重化ツールの backend」｡
- **statusline の配線は 2 通り｡** `statusLine.command` をバイナリの `statusline` に向ける (描画と state file の
  書き込みを同じプロセスで行う) か､自前の statusline から stdin を `ingest-statusline` へ渡す 1 行を足す｡
  取り出しと書式はどちらもバイナリ (`internal/sessionstate`) が持つ｡
  書式は [architecture.md](references/architecture.md) の「statusline が書く state」にある｡
  配線が無ければ上限側のゲートが働かず､block と session の対応も引けない｡
- statusline が書き出すのは Claude Code が statusline の stdin で渡す `rate_limits.five_hour` と
  `context_window.used_percentage`､環境変数 `REX_SESSION` / `REX_BLOCK`｡`resets_at` は Unix epoch 秒で､
  これがあるので画面の時刻表記をパースしなくて済む｡
- hook の配線元は plugin の `hooks/hooks.json` (SessionStart だけ)｡
- session_id の解決は `CLAUDE_CODE_SESSION_ID` を唯一の情報源にする
  (`scripts/get-session-id.sh`)｡プロジェクトの JSONL の mtime から推測すると､
  同一プロジェクトで複数セッションが動いているときに他セッションの state file を読む｡
- 設定は `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`｡
  ランタイム状態 (`daemon.pid` / `status.json` / `rate-limits/` / `context/` / `cost/` / `logs/`) は
  `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` に置く｡
  ログの rotate と古いファイルの削除は daemon 自身が行う｡
- バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` に置く｡plugin の
  install 先はバージョンごとに変わるため､その外の固定パスにする｡用意するのは SessionStart hook で､
  詳細は [architecture.md](references/architecture.md) の「バイナリの用意と差し替え」｡
- sandbox 内のセッションから `stop` を実行すると SIGTERM が
  `operation not permitted` で弾かれる｡デーモン側の後始末は正常なので､
  通常のターミナルから実行するか自己終了に任せる｡

## references

`Read` で開く｡

| ファイル | 中身 |
| ---- | ---- |
| [architecture.md](references/architecture.md) | statusline / hook / daemon の連動､バイナリの用意と差し替え､多重化ツールの backend (rex)､毎 tick の判定表､出力されるファイル､ログの rotate |
| [rate-limit.md](references/rate-limit.md) | ネイティブ auto-continue との分担､limit 検知の二重シグナルとゲート､待機時刻の決め方 |
| [pane-io.md](references/pane-io.md) | 画面の読み取り元､送信のキーストロークと文面の制約 |
| [program-status.md](references/program-status.md) | rex の `program_status` (OSC 7501) と `agent_status` の対応､record を使う条件 (owner.pid と foreground の pid の突き合わせ)､payload の書式 |
| [operations.md](references/operations.md) | コマンドと `--dry-run`､実機での検証順序､設定キー一覧､既知の制約 |

## 症状から引くところ

<!-- cc-premise: 2.1.296 hooks claude --debug が hook ごとの所要時間を Hooks: 行で出す -->

| 症状 | 見るところ |
| ---- | ---- |
| install したばかり､前提がそろっているか分からない | `agents-daemon:setup` skill |
| 上限が解除されても再開しない | rate-limit.md の「待機時刻の決め方」「ゲート」 |
| 関係ない pane にプロンプトが打ち込まれた | rate-limit.md の「limit の判定は 2 つの手がかりを要求する」「ゲート」 |
| `doctor` の pane 一覧が全部 `agent_status=unknown`､daemon.log に `agent_status を判定できません` が出る (rex を上げた直後) | program-status.md の「record を使う条件」｡rex の record の形が変わった疑い |
| daemon が起きない・バイナリがビルドされない | architecture.md の「バイナリの用意と差し替え」､`build.log` |
| 直したコードが動いていない | architecture.md の「バイナリの用意と差し替え」 |
| 送信が block に届かない | pane-io.md､operations.md の「既知の制約」(block 内 tmux､rex 不在) |
| `doctor` の pane 一覧が `session_id=(なし)` になる | statusline が新しいバイナリ (`statusline` か `ingest-statusline`) を呼んでいるか､`context/<sid>.json` に `multiplexer` があるか｡architecture.md の「statusline が書く state」 |
| rex のセッションなのに daemon が起きない | hook のログに `REX_SESSION が無いため` と出ていれば rex の外で起動したセッション｡`doctor` の daemon の節で起動中の daemon の pid を見る |
| `ListAgents` / `SendMessage` がツール一覧に無く `/list-agents` も認識されない (peer へ作業を渡せない) | `agents-daemon:setup` skill の「peer messaging」 |
| hook が 10 秒の timeout に当たっている疑い (prompt の送信が遅い､hook の効果が出ない) | `claude --debug` で起動し､`Hooks: SessionStart [...] finished with status 0 (31ms)` の行 (Claude Code 2.1.296+) で hook ごとの所要時間を見る｡`timed out after <N>ms` なら当たっている |
| 圧縮後に作業が再開しない､離席中に context が圧縮された | daemon の役目ではない｡Claude Code 本体の autocompact / idle compaction (`/config` の `Idle compaction`) の挙動 |
