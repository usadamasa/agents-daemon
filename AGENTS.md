# AGENTS.md

利用上限からの自動再開と context の自動 compact を担う常駐デーモンの Claude Code plugin｡
Go のデーモンと､それを配線する hook・skill を持つ｡

## レイアウト

| パス | 中身 |
| ---- | ---- |
| `.claude-plugin/plugin.json` | plugin manifest｡version の実体はここ 1 箇所｡marketplace.json は agents-marketplace リポジトリが持つ |
| `hooks/hooks.json` | SessionStart / PostCompact / UserPromptSubmit / Stop の配線 |
| `hooks/ensure-daemon.sh`, `hooks/build-daemon.sh` | バイナリの用意 (detach build) と `daemon --ensure` |
| `hooks/compaction-recovery.sh`, `hooks/userpromptsubmit-compaction-recovery.sh` | 圧縮完了 marker と復旧ガイドの注入 |
| `hooks/cache-state.sh` | Stop の stdin を `ingest-stop` へ渡す (prompt cache の状態を `cache/` へ) |
| `hooks/ttl-guard.sh` | UserPromptSubmit の stdin を `ttl-guard` へ渡し､cache 失効後の最初の prompt を止める (exit 2) |
| `hooks/lib/` | hook 共通の logger､marker の置き場 (`compact-markers.sh`)､バイナリの置き場 (`daemon-bin.sh`) |
| `scripts/get-session-id.sh` | compact-prep / setup skill が呼ぶ session_id の取得 |
| `skills/compact-prep/` | `/compact` 前の state file 保存 |
| `skills/agents-daemon/` | 運用と切り分けの手引き｡配線・判定・設定キーの詳細は `references/` |
| `skills/setup/` | install 後の前提確認 (herdr､go/jq､statusline の state file､バイナリ)｡statusline に足す `ingest-statusline` の 1 行は `references/` |
| `cmd/agents-daemon/` | Go の main (cobra) |
| `internal/` | daemon の本体｡パス定数は `internal/apppath` |
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
- 前提条件 (`go` など) が無ければエラー終了する｡警告してスキップしない｡
- データを読む・加工する処理 (transcript､JSON) はバイナリのサブコマンドに持たせ､hook は stdin を渡すだけにする
  (`ingest-statusline` / `ingest-stop`)｡bash 3.2 と jq で timestamp や大きなファイルを扱わない｡

## skill を書くときの決めごと

- script は `"${CLAUDE_PLUGIN_ROOT}/scripts/..."` と書く｡置換されるのは Claude が読む本文だけで､
  Bash の環境変数としては export されない｡frontmatter で置換されるかは確かめていないので､
  `allowed-tools` にパスを書かない｡
- skill 間の参照とスラッシュコマンドは名前空間付きで書く (`agents-daemon:compact-prep`､
  `/agents-daemon:compact-prep`)｡bare 名は `~/.claude/skills/` に残った旧コピーへ当たる｡
- skill 内の references は相対リンクで書き､読む手順は `Read` で書く｡

## パス定数の対

hook・skill・daemon は別プロセスで､ファイル越しに繋がる｡置き場は次の対が同じ規則で持つ｡
片方だけ変えると protocol が黙って壊れる｡

| hook / skill 側 | daemon 側 | 中身 |
| ---- | ---- | ---- |
| `hooks/lib/compact-markers.sh` | `internal/apppath` | `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/` 配下の `compacted/` と `compact-state/` |
| `hooks/lib/daemon-bin.sh` | `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` |
| `skills/compact-prep/SKILL.md` の保存先 | `internal/apppath` | `compact-state/<session_id>.md` |
| `.claude-plugin/plugin.json` の skill 名 | `internal/config` の `compactAutoPrepMessage` 既定 | `/agents-daemon:compact-prep` |
| 利用者の statusline に足す 1 行 (`skills/setup/references/statusline.md`) | `hooks/lib/daemon-bin.sh` と `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon ingest-statusline`｡`context/` `rate-limits/` の書式は `internal/sessionstate` が読み書き両方で持つ |
| `hooks/cache-state.sh` (Stop hook) | `hooks/lib/daemon-bin.sh` | 同じバイナリの `ingest-stop`｡`cache/` の書式と transcript の読み方は `internal/sessionstate` が持つ |
| `hooks/ttl-guard.sh` (UserPromptSubmit hook) | `hooks/lib/daemon-bin.sh` | 同じバイナリの `ttl-guard`｡exit 2 だけが「止める」で､それ以外は通す｡`cache-ack/` の書式と判定は `internal/sessionstate` が持つ (daemon が書き､`ttl-guard` が読む) |

## リリース

tagpr (`.tagpr`､`.github/workflows/tagpr.yaml`) がリリース PR を作る｡`version` は手で上げない｡
tag に **`v` を付けない｡** `v2026.0926.0` は Go module が major 2026 の semver と解釈して拒否する｡
