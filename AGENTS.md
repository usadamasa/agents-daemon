# AGENTS.md

端末多重化ツール (herdr / rex) の pane 越しに､利用上限からの自動再開と context の自動 compact を担う
常駐デーモンの Claude Code plugin｡
Go のデーモンと､それを配線する hook・skill を持つ｡

## レイアウト

| パス | 中身 |
| ---- | ---- |
| `.claude-plugin/plugin.json` | plugin manifest｡version の実体はここ 1 箇所｡marketplace.json は agents-marketplace リポジトリが持つ |
| `hooks/hooks.json` | SessionStart / PostCompact / UserPromptSubmit / Stop の配線 |
| `hooks/ensure-daemon.sh`, `hooks/build-daemon.sh` | バイナリの用意 (detach build) と `daemon --ensure` |
| `hooks/compaction-recovery.sh`, `hooks/userpromptsubmit-compaction-recovery.sh` | 圧縮完了 marker と復旧ガイドの注入 |
| `hooks/ttl-guard.sh` | UserPromptSubmit の stdin を `ttl-guard` へ渡し､cache 失効後の最初の prompt を止める (exit 2) |
| `hooks/herdr-title.sh` | UserPromptSubmit / Stop の stdin を `herdr-title` へ渡し､セッション名とブランチを herdr の space 欄とタブ行へ写す (`internal/hookreport`) |
| `hooks/lib/` | hook 共通の logger､marker の置き場 (`compact-markers.sh`)､バイナリの置き場 (`daemon-bin.sh`) |
| `scripts/get-session-id.sh` | compact-prep / setup skill が呼ぶ session_id の取得 |
| `skills/compact-prep/` | `/compact` 前の state file 保存 |
| `skills/agents-daemon/` | 運用と切り分けの手引き｡配線・判定・設定キーの詳細は `references/` |
| `skills/setup/` | install 後の前提確認 (herdr か rex､go/jq､statusline の state file､バイナリ､peer messaging)｡statusline の配線 2 通りと peer messaging の切り分けは `references/` |
| `skills/peer-sessions/` | 別セッション (peer) へ作業を渡す手順 (`ListAgents` / `SendMessage`)｡多重化ツールごとの pane / block 操作は `references/herdr.md` `references/rex.md` |
| `skills/herdr/` | herdr の CLI の手引き｡`herdr --skill` の出力をそのまま置いた生成物で､手で編集しない｡herdr を更新したら `task herdr-skill` で作り直す (`herdr --version` を commit message に書く) |
| `cmd/agents-daemon/` | Go の main (cobra) |
| `internal/` | daemon の本体｡パス定数は `internal/apppath`｡statusline の描画は `internal/statusline`､cost の台帳は `internal/spend`､sidecar の原子的な書き込みは `internal/statefile` |
| `internal/multiplexer`, `internal/herdrcli`, `internal/rexcli`, `internal/backend` | pane 操作の interface (`multiplexer.Client`) とその herdr / rex 実装｡`backend` が環境 (`REX_SESSION`) で選ぶ｡`herdrcli` は hook が使う socket の client (`SocketClient`) も持つ |
| `internal/hookreport` | hook から呼ばれてセッションの状態を多重化ツールへ写す処理 (`herdr-title`)｡hook script は stdin を渡すだけ |
| `tests/` | hook と script の bats テスト (`tests/lib/`､`tests/fixtures/`) |

## 検証

```sh
task test                                   # go test ./... と bats tests/
task lint                                   # Go の全静的解析｡aqua のツールが要る
shellcheck hooks/*.sh hooks/lib/*.sh scripts/*.sh tests/lib/*.bash tests/fixtures/fake-go .envrc
claude plugin validate --strict .           # plugin manifest と hooks.json
claude plugin validate --strict skills      # skill の frontmatter
```

hook のテストは fake の `go` とバイナリで回るので､Go のビルドが通らなくても hook 側だけ確かめられる｡
bats は hook を `/bin/bash` で起動する (本番の shebang と同じ bash 3.2 で非互換を拾うため)｡

## hook を書くときの決めごと

- hook の stdout は Claude Code が読む｡SessionStart の stdout は会話の context へ入るので､ログは stderr へ出す｡
- SessionStart は 5 秒以内に返す｡`go build` のような重い処理は `nohup ... </dev/null >>log 2>&1 &` で detach する｡
- hook は `${CLAUDE_PLUGIN_ROOT}` に頼らず､自分の位置 (`$(dirname "$0")`) から plugin root を解決する｡
  hooks.json では `"\"${CLAUDE_PLUGIN_ROOT}/hooks/...\""` と引用符で囲む (validate が未引用を警告する)｡
- データを読む・加工する処理 (transcript､JSON) はバイナリのサブコマンドに持たせ､hook は stdin を渡すだけにする
  (`ingest-statusline` / `ttl-guard`)｡bash 3.2 と jq で timestamp や大きなファイルを扱わない｡
- hook から herdr を叩くときは CLI (`herdr`) ではなく `HERDR_SOCKET_PATH` の socket を使う (`herdrcli.SocketClient`)｡
  hook は Claude Code の PATH を継承し､非対話で起動されたセッションでは homebrew のパスが載らない｡
  daemon が CLI を使うのは逆で､detach 後の長い稼働で起動時の socket path が古くなりうるため｡
- Stop hook で transcript を読まない｡transcript は非同期に書かれ､Stop の時点ではターンの最後の応答を
  含まないことがある (Claude Code の hooks のドキュメント)｡cache の状態は daemon が tick で読む｡

## skill を書くときの決めごと

- script は `"${CLAUDE_PLUGIN_ROOT}/scripts/..."` と書く｡置換されるのは Claude が読む本文だけで､
  Bash の環境変数としては export されない｡frontmatter で置換されるかは確かめていないので､
  `allowed-tools` にパスを書かない｡
- skill 間の参照とスラッシュコマンドは名前空間付きで書く (`agents-daemon:compact-prep`､
  `/agents-daemon:compact-prep`)｡bare 名は `~/.claude/skills/` に残った旧コピーへ当たる｡
- skill 内の references は相対リンクで書き､読む手順は `Read` で書く｡

## 常駐物 (state を残すもの) を書くときの決めごと

ランタイム状態 (PID､status､ログ､他プロセスと受け渡す state) は `~/.claude/` に置かず XDG Base Directory に置く｡
state は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`､
再生成できるものは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/`､
設定は `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/`｡`~/.claude/` はハーネス自身が管理する実ディレクトリで､
そこへ揮発物を混ぜると消してよいものと管理されたものの区別が付かなくなる｡実装済みの一覧と rotate の挙動は
`skills/agents-daemon/references/architecture.md` の「出力されるファイル」が持つ｡

- パス解決は `internal/apppath` に閉じ､環境から埋める入口は `apppath.FromEnv` の 1 つだけ｡`os.Getenv` を各所で呼ばない｡
  テストは `apppath.New(t.TempDir(), "", "")` で起点を差し替える｡shell (hook) は `${XDG_STATE_HOME:-$HOME/.local/state}`
  と同じ規則で解決し､bats は `HOME` だけでなく `XDG_STATE_HOME` も一時ディレクトリへ向ける｡
- 書くファイルを足すときは､同じ PR で消す仕組みと architecture.md の表の行 (書く主体・いつ・消す主体) を足す｡
  残す仕組みだけ先に入れると､後から「どこまで消してよいか」を判断する材料が失われる｡
- 現行ログは固定名 (`daemon.log`) にし､日付が変わって最初の書き込みで `daemon.log.YYYY-MM-DD` へ退避する｡
  `tail -f` や `logs` サブコマンドの参照先を日付で動かさない｡
- 保持期間は mtime で判定し､設定キーにせず定数 (`internal/daemon/housekeeping.go`) に置く｡判定に効かない長さを選び､
  根拠をコメントに書く｡
- 削除は daemon 自身が起動時と 1 時間ごとに行う｡cron や別 hook を増やさない｡何かを消したときだけログに残す｡
- 原子的な書き込み (`internal/statefile`) の途中で書き手が死ぬと一時ファイルが残る｡同じディレクトリの一時ファイルの
  接頭辞も削除対象に含める｡
- sandbox 内の Bash から手で叩く (`--foreground`､`simulate`) には利用者の settings の `sandbox.filesystem.allowWrite` に
  state ディレクトリが要る｡hook と statusline は sandbox の外で動くので､無くても本番の書き込みは通る｡

## パス定数の対

hook・skill・daemon は別プロセスで､ファイル越しに繋がる｡置き場は次の対が同じ規則で持つ｡
片方だけ変えると protocol が黙って壊れる｡

| hook / skill 側 | daemon 側 | 中身 |
| ---- | ---- | ---- |
| `hooks/lib/compact-markers.sh` | `internal/apppath` | `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` 配下の `compacted/` と `compact-state/` |
| `hooks/lib/daemon-bin.sh` | `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` |
| `skills/compact-prep/SKILL.md` の保存先 | `internal/apppath` | `compact-state/<session_id>.md` |
| `.claude-plugin/plugin.json` の skill 名 | `internal/config` の `compactAutoPrepMessage` 既定 | `/agents-daemon:compact-prep` |
| 利用者の `statusLine.command`､または自前の statusline に足す 1 行 (`skills/setup/references/statusline.md`) | `hooks/lib/daemon-bin.sh` と `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon statusline` (または `ingest-statusline`)｡`context/` `rate-limits/` の書式は `internal/sessionstate` が読み書き両方で持つ |
| `statusline` / `ingest-statusline` が `context/<sid>.json` の `multiplexer` に書く場所 (環境変数 `REX_SESSION` / `REX_BLOCK` / `HERDR_PANE_ID` から) | `internal/sessionstate` の `ListMultiplexerBlocks` を `internal/herdrcli` と `internal/rexcli` が引く (`internal/backend` が配線) | herdr の pane / rex の block から session_id への対応｡herdr の `agent_session` は読まない｡書式と読み方は `internal/sessionstate/multiplexer.go` が両方持つ |
| statusline の stdin の `transcript_path` (`statusline` / `ingest-statusline` が `context/` へ写す) | `internal/sessionstate` の `CacheLoader` | daemon が辿る transcript｡読み方 (`lastMainResponse`) は `ttl-guard` と `statusline` の ⏱ (cache の残り時間) と共有する |
| 利用者の settings の `env` の `CLAUDE_STATUSLINE_SPEND_CAP` | `cmd/agents-daemon/cmd_statusline.go` の `spendCapEnv` | 月額上限 (USD)｡あるときだけ `statusline` が `cost/` の台帳 (`internal/spend`) を書き､📅 / 🧾 を出す |
| `hooks/ttl-guard.sh` (UserPromptSubmit hook) | `hooks/lib/daemon-bin.sh` | 同じバイナリの `ttl-guard`｡exit 2 だけが「止める」で､それ以外は通す｡`cache-ack/` の書式と判定は `internal/sessionstate` が持つ (daemon が書き､`ttl-guard` が読む) |

## リリース

tagpr (`.tagpr`､`.github/workflows/tagpr.yaml`) がリリース PR を作る｡`version` は手で上げない｡
tag に **`v` を付けない｡** `v2026.0926.0` は Go module が major 2026 の semver と解釈して拒否する｡

リリース PR の merge 後の plugin 更新 (`claude plugin marketplace update usadamasa`､
`claude plugin update agents-daemon@usadamasa`) は sandbox が `~/.claude/plugins` への書き込みを止めるので
利用者が `!` で打つ｡動いている daemon の入れ替えも `!` で行う｡コマンドは
`skills/agents-daemon/references/operations.md` の「plugin の更新後に daemon を入れ替える」｡
