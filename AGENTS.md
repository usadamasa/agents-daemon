# AGENTS.md

端末多重化ツール rex の block 越しに､利用上限からの自動再開と context の自動 compact を担う
常駐デーモンの Claude Code plugin｡
Go のデーモンと､それを配線する hook・skill を持つ｡

## レイアウト

| パス | 中身 |
| ---- | ---- |
| `.claude-plugin/plugin.json` | plugin manifest｡version の実体はここ 1 箇所｡marketplace.json は agents-marketplace リポジトリが持つ |
| `hooks/hooks.json` | SessionStart / PostCompact / UserPromptSubmit の配線 |
| `hooks/ensure-daemon.sh`, `hooks/build-daemon.sh` | バイナリの用意 (detach build) と `daemon --ensure` |
| `hooks/compaction-recovery.sh`, `hooks/userpromptsubmit-compaction-recovery.sh` | 圧縮完了 marker と復旧ガイドの注入 |
| `hooks/ttl-guard.sh` | UserPromptSubmit の stdin を `ttl-guard` へ渡し､cache 失効後の最初の prompt を止める (exit 2) |
| `hooks/lib/` | hook 共通の logger､marker の置き場 (`compact-markers.sh`)､バイナリの置き場 (`daemon-bin.sh`) |
| `scripts/get-session-id.sh` | compact-prep / setup skill が呼ぶ session_id の取得 |
| `skills/compact-prep/` | `/compact` 前の state file 保存 |
| `skills/agents-daemon/` | 運用と切り分けの手引き｡配線・判定・設定キーの詳細は `references/` |
| `skills/setup/` | install 後の前提確認｡backend ごとの手順と配線の詳細は `references/` |
| `skills/peer-sessions/` | 別セッション (peer) へ作業を渡す手順 (`ListAgents` / `SendMessage`)｡rex の block 操作は `references/rex.md` |
| `cmd/agents-daemon/` | Go の main (cobra) |
| `internal/` | daemon の本体｡パス定数は `internal/apppath`｡statusline の描画は `internal/statusline`､cost の台帳は `internal/spend`､sidecar の原子的な書き込みは `internal/statefile` |
| `internal/multiplexer`, `internal/rexcli`, `internal/backend` | pane 操作の interface (`multiplexer.Client`) とその rex 実装｡`backend` が環境に `REX_SESSION` があることを確かめる (無ければ `daemon --ensure` は何もせず戻る) |
| `tests/` | hook と script の bats テスト (`tests/lib/`､`tests/fixtures/`) |
| `flake.nix` | 開発ツールの devShell (版は `flake.lock` が固定する)。nixpkgs に無い spm-go はここでビルドする |
| `.claude/skills/catching-up-with-claude-code-changelog/` | Claude Code の更新後に CHANGELOG・バイナリ・docs を追い､この repo の前提 (画面の文言､hook と statusline の field､本体の新機能との重なり) を点検して issue に落とす手順｡plugin には同梱しない保守用 skill |
| `.claude/skills/catching-up-with-rex-changelog/` | rex の更新後に changelog (superlogical MCP) と自分の block への固定コマンドの応答を `rex-premise` marker と `internal/rexcli` の fixture と突き合わせ､ずれを issue に落とす手順｡前回見た版は `references/last-checked.md`｡plugin には同梱しない保守用 skill |

## 検証

```sh
task test                                   # go test ./... と bats tests/
task lint                                   # Go の全静的解析。devShell のツールが要る
shellcheck hooks/*.sh hooks/lib/*.sh scripts/*.sh tests/lib/*.bash tests/fixtures/fake-go .envrc
claude plugin validate --strict .           # plugin manifest と hooks.json
claude plugin validate --strict skills      # skill の frontmatter
```

hook のテストは fake の `go` とバイナリで回るので､Go のビルドが通らなくても hook 側だけ確かめられる｡
bats は hook を `/bin/bash` で起動する (本番の shebang と同じ bash 3.2 で非互換を拾うため)｡

Go と lint・テストのツールは `flake.nix` の devShell が揃え、`.envrc` (nix-direnv の `use flake`) が読み込む。
devShell の go は `go.mod` の `go` 行と同じ系列 (`go_1_26`) にし、`GOTOOLCHAIN=local` でずれを即座に落とす。
版を上げるときは `nix flake update`。devShell は開発用で、利用者の環境で hook がバイナリをビルドするときは
利用者の PATH の go を使う。

## hook を書くときの決めごと

<!-- cc-premise: 2.1.299 hooks SessionStart の stdout は context に入る｡UserPromptSubmit の既定 timeout は 30 秒 -->

- hook の stdout は Claude Code が読む｡SessionStart の stdout は会話の context へ入るので､ログは stderr へ出す｡
- SessionStart は 5 秒以内に返す｡`go build` のような重い処理は `nohup ... </dev/null >>log 2>&1 &` で detach する｡
- hook は `${CLAUDE_PLUGIN_ROOT}` に頼らず､自分の位置 (`$(dirname "$0")`) から plugin root を解決する｡
  hooks.json では `"\"${CLAUDE_PLUGIN_ROOT}/hooks/...\""` と引用符で囲む (validate が未引用を警告する)｡
- データを読む・加工する処理 (transcript､JSON) はバイナリのサブコマンドに持たせ､hook は stdin を渡すだけにする
  (`ingest-statusline` / `ttl-guard`)｡bash 3.2 と jq で timestamp や大きなファイルを扱わない｡
- hook から多重化ツールを叩かない｡hook は Claude Code の PATH を継承し､非対話で起動されたセッションでは
  homebrew のパスが載らない｡多重化ツールへの操作は daemon に持たせる｡
- Stop hook で transcript を読まない｡transcript は非同期に書かれ､Stop の時点ではターンの最後の応答を
  含まないことがある (Claude Code の hooks のドキュメント)｡cache の状態は daemon が tick で読む｡

## skill を書くときの決めごと

- script は `"${CLAUDE_PLUGIN_ROOT}/scripts/..."` と書く｡置換されるのは Claude が読む本文だけで､
  Bash の環境変数としては export されない｡frontmatter で置換されるかは確かめていないので､
  `allowed-tools` にパスを書かない｡
- skill 間の参照とスラッシュコマンドは名前空間付きで書く (`agents-daemon:compact-prep`､
  `/agents-daemon:compact-prep`)｡bare 名は `~/.claude/skills/` に残った旧コピーへ当たる｡
- skill 内の references は相対リンクで書き､読む手順は `Read` で書く｡
- この repo を保守する手順 (利用者の運用ではなく maintainer だけが使うもの) の skill は `.claude/skills/` に置き、
  plugin の `skills/` には入れない。`skills/` に置くと install した全セッションに description が載り、
  validate と tagpr のリリースにも乗る。名前には作業の性質 (追従、changelog など) を入れ、レイアウト表に 1 行足す。
  frontmatter は `claude plugin validate --strict .claude/skills` で確かめる。

## Claude Code の挙動を書くときの決めごと

- 画面の文言､hook の発火条件､statusline の field､OSC のように Claude Code の版で変わりうる挙動を docs・コメント・
  fixture に書くときは､その前提を述べた箇所に marker を 1 行置く｡形は `cc-premise: <version> <topic> [メモ]`
  (Go と sh はコメント､md は `<!-- -->`)｡`<version>` は最後に確かめた Claude Code の版､`<topic>` は
  `.claude/skills/catching-up-with-claude-code-changelog/SKILL.md` の topic 一覧のどれか｡
  skill がこの marker を grep して､更新後に見直す箇所の一覧にする｡確かめ直したら `<version>` を上げる｡
- rex の版で変わりうる挙動 (`program_status` の record の中身､CLI の引数と exit code) も同じ形で
  `rex-premise: <version> <topic> [メモ]` を置く｡`<version>` は changelog の版 (`preview.15`)､`<topic>` は
  `program_status` か `cli`｡rex を上げたら `.claude/skills/catching-up-with-rex-changelog/SKILL.md` の手順で
  一覧し､changelog (superlogical MCP の `read_changelog`) と実機の応答と突き合わせる｡

## state を書くときの決めごと

- ランタイム状態は `~/.claude/` に置かず XDG (`internal/apppath`) に置く｡環境から埋める入口は `apppath.FromEnv` だけ｡
- 書くファイルを足すときは､同じ PR で消す仕組みと architecture.md「出力されるファイル」の行 (書く主体・消す主体) を足す｡
- 削除は daemon 自身が mtime で行う (`internal/daemon/housekeeping.go`)｡保持期間は定数にし､根拠をコメントに書く｡

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
| `statusline` / `ingest-statusline` が `context/<sid>.json` の `multiplexer` に書く場所 (環境変数 `REX_SESSION` / `REX_BLOCK` から) | `internal/sessionstate` の `ListMultiplexerBlocks` を `internal/rexcli` が引く (`internal/backend` が配線) | rex の block から session_id への対応｡書式と読み方は `internal/sessionstate/multiplexer.go` が両方持つ |
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
