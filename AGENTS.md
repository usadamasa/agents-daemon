# AGENTS.md

利用上限からの自動再開と context の自動 compact を担う常駐デーモンの Claude Code plugin｡
Go のデーモン 1 本と､それを配線する hook 3 本・skill 3 本を持つ｡

## レイアウト

| パス | 中身 |
| ---- | ---- |
| `.claude-plugin/plugin.json` | plugin manifest｡version の実体はここ 1 箇所｡marketplace.json は agents-marketplace リポジトリが持つ |
| `hooks/hooks.json` | SessionStart / PostCompact / UserPromptSubmit の配線 |
| `hooks/ensure-daemon.sh`, `hooks/build-daemon.sh` | バイナリの用意 (detach build) と `daemon --ensure` |
| `hooks/compaction-recovery.sh`, `hooks/userpromptsubmit-compaction-recovery.sh` | 圧縮完了 marker と復旧ガイドの注入 |
| `hooks/lib/` | hook 共通の logger､marker の置き場 (`compact-markers.sh`)､バイナリの置き場 (`daemon-bin.sh`) |
| `scripts/get-session-id.sh` | compact-prep / setup skill が呼ぶ session_id の取得 |
| `skills/compact-prep/` | `/compact` 前の state file 保存 |
| `skills/agents-daemon/` | 運用と切り分けの手引き｡配線・判定・設定キーの詳細は `references/` |
| `skills/setup/` | install 後の前提確認 (herdr､go/jq､statusline の state file､バイナリ) と statusline への配線 |
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

## リリース

CalVer (`YYYY.0M0D.MICRO`) の tag を手で打つ｡**`v` を付けない｡**
`v2026.0926.0` は Go module が major 2026 の semver と解釈し､module path に
`/v2026` が無いと拒否する｡`v` 無しなら Go にとって semver tag ではないので無視される｡

1. `.claude-plugin/plugin.json` の `version` を上げて merge する｡
   SessionStart hook はこの値と cache の stamp を比べて､利用者の手元のバイナリを建て直す｡
2. tag と release を作る｡

```sh
git tag 2026.0926.0 && git push origin 2026.0926.0
gh release create 2026.0926.0 --generate-notes
```
