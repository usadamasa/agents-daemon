---
name: agents-daemon
description: >-
  agents-daemon plugin (Claude Code のセッションのそばに常駐し、セッションの中からはできないことを背後で担う
  デーモン) の運用と切り分けに使う。利用上限からの自動再開が期待どおり動かない、daemon やバイナリが動いていない
  疑いがある、コマンドや設定キーを確かめたい、plugin の更新後に daemon を入れ替えたい、といったときに引く。
---

# agents-daemon

agents-daemon の運用と切り分けの手引き。役割の区分と「本体ができることは本体に任せる」境界は
plugin root の `README.md` の「役割」が持つ。コマンド、設定キー、既知の制約は
[operations.md](references/operations.md) が持つ。

## 症状から引くところ

<!-- cc-premise: 2.1.296 hooks claude --debug が hook ごとの所要時間を Hooks: 行で出す -->

| 症状 | 見るところ |
| ---- | ---- |
| 上限が解除されても再開しない | `logs` の「待機に入りました」と起床時刻の行。operations.md の「既知の制約」(statusline の未配線、ネイティブ auto-continue への譲り、spend limit) |
| 関係ない pane にプロンプトが打ち込まれた | すぐ `stop` し、`logs` の該当行と画面を添えて issue にする |
| `doctor` の pane 一覧が全部 `agent_status=unknown` (rex を上げた直後) | `doctor` の「rex CLI の前提」の `⚠`。rex の版への追従が要るので issue にする |
| daemon が起きない・バイナリがビルドされない | `$STATE/logs/build.log`。operations.md の「既知の制約」(go が PATH に無い、rex の外) |
| plugin を更新したのに古い daemon が動いている | operations.md の「plugin の更新後に daemon を入れ替える」 |
| 送信が block に届かない | operations.md の「既知の制約」(block 内 tmux、rex の送信は未測定) |
| `doctor` の pane 一覧が `session_id=(なし)` になる | statusline の配線 (`agents-daemon:setup` skill の `references/statusline.md`) |
| rex のセッションなのに daemon が起きない | `doctor` の daemon の節。`REX_SESSION` の無い起動では起こさない |
| `stop` が `operation not permitted` で弾かれる | operations.md の「既知の制約」 |
| 放置した worktree が回収されない、今すぐ回収したい、`prune-worktrees` が `operation not permitted` で落ちる | operations.md の「放置された worktree を回収する」 |
| hook が 10 秒の timeout に当たっている疑い (prompt の送信が遅い、hook の効果が出ない) | `claude --debug` で起動し、`Hooks: SessionStart [...] finished with status 0 (31ms)` の行 (Claude Code 2.1.296+) で hook ごとの所要時間を見る。`timed out after <N>ms` なら当たっている |
| 圧縮後に作業が再開しない、離席中に context が圧縮された | daemon の役目ではない。Claude Code 本体の autocompact / idle compaction (`/config` の `Idle compaction`) の挙動 |

`$STATE` は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`。

表に無い症状は次の skill が持つ。

- install 直後の前提の確認と statusline / peer messaging の配線は `agents-daemon:setup` skill
- `dream harvest` (Stop hook) と記憶の consolidation は `agents-daemon:dream` skill
- peer への送受信は `agents-daemon:peer-sessions` skill

## references

`Read` で開く。

| ファイル | 中身 |
| ---- | ---- |
| [operations.md](references/operations.md) | コマンド、設定キー、既知の制約 |
