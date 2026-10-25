# AGENTS.md

Claude Code のセッション群のそばに常駐し、セッションの中からはできないことを背後で担うデーモンの Claude Code plugin。
担うのは止まったセッションへの働きかけ、セッションを跨いだ状態の集約、時間で動く掃除、多重化ツール rex への操作。
Go のデーモンと、それを配線する hook・skill を持つ。役割の区分と「本体ができることは本体に任せる」境界は
README の「役割」が持つ。機能を足す・外す判断はそこに照らす。

## レイアウト

`internal/` のパッケージは、非テストの呼び手が 1 つならその呼び手の下に入れ子にする。複数の読み手がいる書式は、
書き手の下に読み書きを置く (`internal/dream/harvest/record`)。

| パス | 中身 |
| ---- | ---- |
| `.claude-plugin/plugin.json` | plugin manifest。version の実体はここ 1 箇所。marketplace.json は agents-marketplace リポジトリが持つ |
| `hooks/hooks.json` | SessionStart、Stop、PostToolUse の配線と、mod の読み込み (`modules` の `hooks/register.ts`) |
| `hooks/register.ts`, `hooks/daemon-bin.ts` | mod の入口。deps、park、notice、handoff-notice の hook を束ね、`session.start` を持つ。`daemon-bin.ts` はバイナリの置き場 |
| `hooks/deps.tsx`, `hooks/deps.test.tsx`, `types/index.d.ts` | 依存図を dock の pane に描く mod (Claude Code の mods)。`/deps` で開き、`agents-daemon deps` の JSON を数秒ごとに読み直す。ノードを選んで `$.session.send` でそのタスクのセッションへ指示を送る。`types/` は mod が `$.state` に置く値の型。テストは `claude plugin test .` |
| `hooks/graph.ts` | 依存の DAG の記号と凡例、数の行、レーンの行の分け方、Add blocker の候補、辺の無い渡し先 |
| `hooks/park.tsx`, `hooks/park.test.tsx` | model が呼べる tool `park` (パーキングロットにメモを置く) |
| `hooks/deps-data.ts` | `agents-daemon deps` を読んで deps に置く (pane と handoff-notice が使う) |
| `hooks/parking.ts` | メモの見せ方 (時刻の JST 表示を含む) と pane からの操作 (捨てる、タスクにする、issue にするよう頼む、パーキングへ移す) |
| `hooks/notice.ts`, `hooks/notice.test.tsx` | Read / Edit / Write の結果に、触ったファイルに関係するパーキングのメモを model 向けに添える。compact で伝え直す |
| `hooks/handoff-notice.ts`, `hooks/handoff-notice.test.tsx` | `SendMessage` の結果に、辺の無い宛先への辺の書き方を model 向けに添える |
| `hooks/log.ts`, `hooks/log.test.tsx` | mod の出来事を `event-log` で出来事のログに残す |
| `hooks/ensure-daemon.sh`, `hooks/build-daemon.sh` | バイナリの用意 (detach build) と `daemon --ensure` |
| `hooks/dream-harvest.sh` | Stop で `dream harvest --detach` を exec する |
| `hooks/prune-worktrees.sh` | SessionStart で `prune-worktrees --hook` に stdin を渡す |
| `hooks/ingest-task.sh` | PostToolUse (Task 系と TodoWrite) で `ingest-task` に stdin を渡す |
| `hooks/lib/` | hook 共通の logger、バイナリの置き場 (`daemon-bin.sh`) |
| `scripts/get-session-id.sh` | setup skill が呼ぶ session_id の取得 |
| `skills/agents-daemon/` | 利用者向けの運用と切り分けの手引き。コマンドと設定キーは `references/operations.md` |
| `skills/setup/` | install 後の前提確認。backend ごとの手順と配線の詳細は `references/` |
| `skills/peer-sessions/` | 別セッション (peer) へ作業を渡す手順 (`ListAgents` / `SendMessage`)。rex の block 操作は `references/rex.md` |
| `skills/dream/` | 記憶階層の consolidation の手引き。実行系の呼び方は `references/pipeline.md` |
| `cmd/agents-daemon/` | Go の main と root の登録。コマンドの組み立ては `internal/<family>cli` (境界は `changing-agents-daemon-internals` skill の architecture.md「CLI の組み立て」) |
| `internal/` | daemon の本体。パス定数は `internal/apppath`。statusline の描画と実行は `internal/statusline`、cost の台帳は `internal/statusline/spend`、daemon の監視ループは `internal/daemon`、daemon の起動の排他と status.json と PID ファイルは `internal/daemon/state`、daemon が書くセッション台帳は `internal/daemon/ledger`、sidecar の原子的な書き込みは `internal/statefile`。前提条件の診断 (`doctor`) は `internal/doctor`、ほかの読むだけの診断 (`inspect` / `status` / `logs`) は `internal/verify`、使い捨て pane へ送る e2e (`simulate`) は `internal/simulate`。dream の実行系は `internal/dream` (harvest の出力の書式は `internal/dream/harvest/record`、mine の出力の書式は `internal/dream/mine/candidates`、mine が読む memory と skill は `internal/dream/mine/corpus`)、放置 worktree の回収は `internal/worktree`、cwd の repo と branch を求めるのは `internal/gitrepo`、タスクの記録は `internal/tasklog`、パーキングロットのメモは `internal/parking`、メモを渡す先の新しいセッションを rex の window に起こすのは `internal/spawn`、セッションをまたぐ辺は `internal/edge`、依存図のノードの key と辺の端点の文法 (Ref) は `internal/ref`、タスクの記録と status.json を依存図に組むのは `internal/deps`、ノードの材料から依存の DAG を組むのは `internal/deps/graph`、依存の DAG の段・下流・輪・鎖を数え、pane のレーンの行を組むのは `internal/deps/dag`、依存図とパーキングロットの経路の出来事のログは `internal/eventlog`、上限の出来事の記録は `internal/daemon/limitlog`、plugin の更新と daemon の建て直し (`update`) は `internal/selfupdate` |
| `internal/multiplexer`, `internal/multiplexer/rexcli`, `internal/backend` | pane 操作の interface (`multiplexer.Client`) とその rex 実装。`backend` が環境に `REX_SESSION` があることを確かめる (無ければ `daemon --ensure` は何もせず戻る) |
| `tests/` | hook と script の bats テスト (`tests/lib/`、`tests/fixtures/`) |
| `ci/mod-types/` | `task typecheck` (手元と CI) が使う mod の型の写し。`task mod-types:sync` (`scripts/sync-mod-types.sh`) が `.claude-plugin/types/` から写す |
| `models/handoff/` | メモの受け渡しとタスクの記録の TLA+ のモデル。`task model-check` (`scripts/model-check.sh`) が TLC で回す。使い方と期待は `models/handoff/README.md` |
| `flake.nix` | 開発ツールの devShell (版は `flake.lock` が固定する)。go-arch-metrics の CLI は go.mod の tool directive が固定する |
| `.claude/skills/changing-agents-daemon-internals/` | 実装の仕組みとその理由 (配線、上限の検知、pane の読み書き、mod、バイナリ、出力されるファイル) と実機での検証。plugin には同梱しない保守用 skill |
| `.claude/skills/catching-up-with-claude-code-changelog/` | Claude Code の更新後に CHANGELOG・バイナリ・docs を追い、この repo の前提 (画面の文言、hook と statusline の field、本体の新機能との重なり) を点検して issue に落とす手順。plugin には同梱しない保守用 skill |
| `.claude/skills/catching-up-with-rex-changelog/` | rex の更新後に changelog (superlogical MCP) と自分の block への固定コマンドの応答を `rex-premise` marker と `internal/multiplexer/rexcli` の fixture と突き合わせ、ずれを issue に落とす手順。前回見た版は `references/last-checked.md`。plugin には同梱しない保守用 skill |

## 検証

```sh
task test                                   # go test ./... と bats tests/
task fuzz                                   # go の fuzz target を 1 つずつ回す (FUZZTIME で時間)
task model-check                            # models/ の TLA+ を TLC で回す (CI では回さない)
task lint                                   # Go の全静的解析。devShell のツールが要る
task vulncheck                              # govulncheck の symbol モード (task lint が呼ぶ)
shellcheck hooks/*.sh hooks/lib/*.sh scripts/*.sh tests/lib/*.bash tests/fixtures/fake-go .envrc
claude plugin validate --strict .           # plugin manifest と hooks.json
claude plugin validate --strict skills      # skill の frontmatter
claude plugin test .                        # mod (hooks/*.test.tsx) のテスト
task typecheck                              # mod の型検査 (tsc)。型は ci/mod-types/ の写し (CI も同じ)
```

`invariants_test.go` と `fuzz_test.go` は、1 場面を固定する例ではなく「どの入力でも破ってはいけないこと」を書いて乱択で
探す実行できる仕様 (`internal/monitor`、`internal/deps/dag`、`internal/detect`、`internal/statefile`、`internal/parking`、
`internal/tasklog`、`internal/edge`)。反例が出たらコードを直すか挙動を正当化し、性質を弱めて通さない。
`models/handoff` の不変条件は `internal/parking` と `internal/tasklog` の `invariants_test.go` が
実物の Store の交錯で持つ。`task test` は fuzz の seed だけを回し、乱択は `task fuzz` で行う。
hook のテストは fake の `go` とバイナリで回るので、Go のビルドが通らなくても hook 側だけ確かめられる。
bats は hook を `/bin/bash` で起動する (本番の shebang と同じ bash 3.2 で非互換を拾うため)。
PR の CI は `.github/workflows/ci.yaml`。コードが変わらなくても新しく公開された脆弱性を拾うため、
`.github/workflows/vulncheck.yaml` が `task vulncheck` を週次で回す。同じ workflow が `task vulncheck:module` も回すが、
修正版の無い到達しない脆弱性でも落ちるので合否に入れず、結果を run の summary に残すだけにする。

Go と lint・テストのツールは `flake.nix` の devShell が揃え、`.envrc` (nix-direnv の `use flake`) が読み込む。
devShell の go は `go.mod` の `go` 行と同じ系列 (`go_1_26`) にし、`GOTOOLCHAIN=local` でずれを即座に落とす。
版を上げるときは `nix flake update`。devShell は開発用で、利用者の環境で hook がバイナリをビルドするときは
利用者の PATH の go を使う。
`flake.lock`・`go.mod`・GitHub Actions の更新は Dependabot が週次で PR を出す (`.github/dependabot.yml`)。

## 文章を書くときの決めごと

- 句読点は全角の「。」「、」を使う。半角の「｡」「､」は使わない (md、コメント、ログとエラーの文面のどれも)。

## hook を書くときの決めごと

<!-- cc-premise: 2.1.310 hooks SessionStart の stdout は context に入る。UserPromptSubmit の既定 timeout は 30 秒 -->

- hook の stdout は Claude Code が読む。SessionStart の stdout は会話の context へ入るので、ログは stderr へ出す。
- SessionStart は 5 秒以内に返す。`go build` のような重い処理は `nohup ... </dev/null >>log 2>&1 &` で detach する。
- hook は `${CLAUDE_PLUGIN_ROOT}` に頼らず、自分の位置 (`$(dirname "$0")`) から plugin root を解決する。
  hooks.json では `"\"${CLAUDE_PLUGIN_ROOT}/hooks/...\""` と引用符で囲む (validate が未引用を警告する)。
- データを読む・加工する処理 (JSON) はバイナリのサブコマンドに持たせ、hook や statusline は stdin を渡すだけにする
  (`ingest-statusline`)。bash 3.2 と jq で timestamp や大きなファイルを扱わない。
- hook から多重化ツールを叩かない。hook は Claude Code の PATH を継承し、非対話で起動されたセッションでは
  homebrew のパスが載らない。多重化ツールへの操作は daemon に持たせる。
- Stop hook で transcript を読まない。transcript は非同期に書かれ、Stop の時点ではターンの最後の応答を
  含まないことがある (Claude Code の hooks のドキュメント)。`dream-harvest.sh` が起こす `dream harvest` が
  JSONL を走査するのはこの懸念に当たらない: 最後の assistant 応答を使わず、mtime で絞って session_id ごとに
  last-wins で書き直すので、書き切れていないセッションは次の harvest で読み直される。

## skill を書くときの決めごと

- script は `"${CLAUDE_PLUGIN_ROOT}/scripts/..."` と書く。置換されるのは Claude が読む本文だけで、
  Bash の環境変数としては export されない。frontmatter で置換されるかは確かめていないので、
  `allowed-tools` にパスを書かない。
- skill 間の参照とスラッシュコマンドは名前空間付きで書く (`agents-daemon:setup`、
  `/agents-daemon:setup`)。bare 名は `~/.claude/skills/` に残った旧コピーへ当たる。
- skill 内の references は相対リンクで書き、読む手順は `Read` で書く。
- この repo を保守する手順 (利用者の運用ではなく maintainer だけが使うもの) の skill は `.claude/skills/` に置き、
  plugin の `skills/` には入れない。`skills/` に置くと install した全セッションに description が載り、
  validate と tagpr のリリースにも乗る。名前には作業の性質 (追従、changelog など) を入れ、レイアウト表に 1 行足す。
  frontmatter は `claude plugin validate --strict .claude/skills` で確かめる。

## Claude Code の挙動を書くときの決めごと

- 画面の文言、hook の発火条件、statusline の field、OSC のように Claude Code の版で変わりうる挙動を docs・コメント・
  fixture に書くときは、その前提を述べた箇所に marker を 1 行置く。形は `cc-premise: <version> <topic> [メモ]`
  (Go と sh はコメント、md は `<!-- -->`)。`<version>` は最後に確かめた Claude Code の版、`<topic>` は
  `.claude/skills/catching-up-with-claude-code-changelog/SKILL.md` の topic 一覧のどれか。
  skill がこの marker を grep して、更新後に見直す箇所の一覧にする。確かめ直したら `<version>` を上げる。
- rex の版で変わりうる挙動 (`program_status` の record の中身、CLI の引数と exit code) も同じ形で
  `rex-premise: <version> <topic> [メモ]` を置く。`<version>` は Mac の changelog の版 (`preview.19`)、`<topic>` は
  `program_status` か `cli`。rex を上げたら `.claude/skills/catching-up-with-rex-changelog/SKILL.md` の手順で
  一覧し、changelog (superlogical MCP の `read_changelog`) と実機の応答に突き合わせる。

## state を書くときの決めごと

- ランタイム状態は `~/.claude/` に置かず XDG (`internal/apppath`) に置く。環境から埋める入口は `apppath.FromEnv` だけ。
- 書くファイルを足すときは、同じ PR で消す仕組みと `changing-agents-daemon-internals` skill の architecture.md「出力されるファイル」の行 (書く主体・消す主体) を足す。
- 削除は daemon 自身が mtime で行う (`internal/daemon/housekeeping.go`)。保持期間は定数にし、根拠をコメントに書く。

## パス定数の対

hook・skill・daemon は別プロセスで、ファイル越しに繋がる。置き場は次の対が同じ規則で持つ。
片方だけ変えると protocol が黙って壊れる。

| hook / skill 側 | daemon 側 | 中身 |
| ---- | ---- | ---- |
| `hooks/lib/daemon-bin.sh` | `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` |
| 利用者の `statusLine.command`、または自前の statusline に足す 1 行 (`skills/setup/references/statusline.md`) | `hooks/lib/daemon-bin.sh` と `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon statusline` (または `ingest-statusline`)。`rate-limits/` の書式は `internal/ratelimit`、`context/` の書式は `internal/sessioncontext` が読み書き両方で持つ |
| `statusline` / `ingest-statusline` が `context/<sid>.json` の `multiplexer` に書く場所 (環境変数 `REX_SESSION` / `REX_BLOCK` から) | `internal/sessioncontext` の `ListMultiplexerBlocks` を `internal/multiplexer/rexcli` が引く (`internal/backend` が配線) | rex の block から session_id への対応。書式と読み方は `internal/sessioncontext` が両方持つ |
| `hooks/daemon-bin.ts` の `daemonBin` (mod が `agents-daemon deps --cwd <cwd>` と `park` を呼ぶ) | `hooks/lib/daemon-bin.sh` と `Taskfile.yml` の `install` | `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`。mod は `$.env.get` で `XDG_CACHE_HOME` と `HOME` を読む |
| `hooks/deps.tsx` の `DepsData` (`types/index.d.ts`) | `internal/deps` の `Deps` (`graph` は `internal/deps/graph` の `Graph`) | `deps` の JSON の形。片方の欄を変えたらもう片方も変える |
| `hooks/park.tsx` の `FIELDS` と `Added` | `internal/parking` の `Request` と `Added` | `park add` の stdin と stdout の JSON の形 |
| `hooks/parking.ts` の `park` と `run` が組む引数 | `cmd/agents-daemon/internal/parkcli/park.go` の `set`、`discard`、`adopt` | `park set <id> --status <status> --from <status>`、`park discard --days <N>`、`park adopt <ノードの key (task:<session_id>/<task_id>)>` に `--session` と `--cwd` を添える。渡す前に `--status handed --from open` で取り、届かなければ `--status open --from handed` で戻す |
| `hooks/deps.tsx` の `unlink` と `addBlocker` が組む引数、`hooks/handoff-notice.ts` の `describe` が model に示す形 | `cmd/agents-daemon/internal/depscli/edge.go` の `rm` と `add` | `edge rm\|add --before <ref> --before-cwd <cwd> --after <ref> --after-cwd <cwd>`。add の ref はノードの key、rm は辺の記録の ref |
| `hooks/notice.ts` の `openMemos` が組む引数と読む欄 | `cmd/agents-daemon/internal/parkcli/park.go` の `list` の `--here` と `internal/parking` の `Item` | `park list --status open --here --session <id> --cwd <cwd>`。stdout は `Item` の配列 (`id`、`repo`、`files`、`sources[].session_id` などを読む) |
| `hooks/parking.ts` の `spawnSession` が組む引数と stdin | `cmd/agents-daemon/internal/parkcli/spawn.go` と `internal/spawn` の `Result` | `spawn --repo <path>`、stdin は最初のターンの本文、stdout は `{session_id, pane_id, foreground, detail}` (foreground が false なら mod はメモを戻さない) |
| `hooks/log.ts` の `logEvent` が組む引数 | `cmd/agents-daemon/internal/depscli/event_log.go` の `event-log` と `internal/eventlog` の `Record` | `event-log --session <id> -- <event> key=value…`。出来事の名前は `internal/eventlog` の package doc |
| 利用者の settings の `env` の `CLAUDE_STATUSLINE_SPEND_CAP` | `internal/statusline/run.go` の `SpendCapEnv` | 月額上限 (USD)。あるときだけ `statusline` が `cost/` の台帳 (`internal/statusline/spend`) を書き、📅 / 🧾 を出す |

## リリース

tagpr (`.tagpr`、`.github/workflows/tagpr.yaml`) がリリース PR を作る。`version` は手で上げない。
tag に **`v` を付けない。** `v2026.0926.0` は Go module が major 2026 の semver と解釈して拒否する。

リリース PR の merge 後の plugin 更新と動いている daemon の入れ替えは、`agents-daemon update` が 1 回で行う。
sandbox が `~/.claude/plugins` と `~/.cache/agents-daemon` への書き込みを止めるので、利用者が `!` で打つ。
コマンドは `skills/agents-daemon/references/operations.md` の「plugin の更新後に daemon を入れ替える」。
