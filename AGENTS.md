# AGENTS.md

Claude Code のセッション群のそばに常駐し、セッションの中からはできないことを背後で担うデーモンの Claude Code plugin。
担うのは止まったセッションへの働きかけ、セッションを跨いだ状態の集約、時間で動く掃除、多重化ツール rex への操作。
Go のデーモンと、それを配線する hook・skill を持つ。役割の区分と「本体ができることは本体に任せる」境界は
README の「役割」が持つ。機能を足す・外す判断はそこに照らす。

## レイアウト

`cmd/` は `internal/` の本体を組み立てる層で、この上下は崩さない。`internal/` のパッケージは、`internal/` の中の
非テストの呼び手が 1 つならその呼び手の下に入れ子にする。呼び手が `cmd/` の cli だけなら `internal/` 直下に置く。
複数の読み手がいる書式は、書き手の下に読み書きを置く (`internal/dream/harvest/record`)。
パッケージは依存のクラスタから読み直した家族 (handoff、runtime、dream、statusline、worktree) か、どの家族からも
使われる共有層のどれかに属し、`.go-arch-lint.yml` の component がその境界を守る (#300)。runtime の家族は
`internal/runtime/` の下に集め、ほかの家族と共有層は `internal/` 直下に並ぶ。新しいパッケージは
どの家族か共有層かを決めて component の `in` に足さないと、`task lint` の `analyze-arch-lint` が uncovered で落ちる。

| パス | 中身 |
| ---- | ---- |
| `.claude-plugin/plugin.json` | plugin manifest。version の実体はここ 1 箇所 |
| `hooks/hooks.json` | hook の配線と mod の読み込み |
| `hooks/*.sh`, `hooks/lib/` | SessionStart・Stop・PostToolUse の hook。stdin をバイナリへ渡すだけ |
| `hooks/*.ts`, `hooks/*.tsx` | mod (依存図の pane、park の tool、メモと辺の通知)。入口は `hooks/register.ts` |
| `hooks/protocol.ts` | 実行時に要る schema (park の tool の inputSchema)。`task protocol:generate` が生成する |
| `types/index.d.ts` | mod が `$.state` に置く値の型と、生成の印の間に schema から生成した JSON の型 |
| `skills/` | plugin が配る利用者向けの skill |
| `cmd/agents-daemon/` | main と root の登録。コマンドの組み立ては `cmd/agents-daemon/internal/<family>cli` |
| `internal/` | daemon の本体。各パッケージの責務は package doc、家族の境界は `.go-arch-lint.yml` |
| `internal/protocol/protocol.schema.json` | mod とバイナリが受け渡す JSON の形の定義。Go の型 (`types.go`) の生成元 |
| `tests/` | hook と script の bats テスト |
| `ci/mod-types/` | `task typecheck` が使う mod の型の写し (`task mod-types:sync`) |
| `models/handoff/` | メモの受け渡しの TLA+ のモデル (`task model-check`) |
| `flake.nix` | 開発ツールの devShell |
| `.claude/skills/` | この repo の保守用 skill。plugin には同梱しない |

## 検証

```sh
task test                                   # go test ./... と bats tests/
task fuzz                                   # go の fuzz target を 1 つずつ回す (FUZZTIME で時間)
task model-check                            # models/ の TLA+ を TLC で回す (CI では回さない)
task lint                                   # Go の全静的解析。devShell のツールが要る
task vulncheck                              # govulncheck の symbol モード (task lint が呼ぶ)
task typecheck                              # mod の型検査 (tsc)
task protocol:generate                      # schema を変えたら Go の型と mod の型を作り直す
shellcheck hooks/*.sh hooks/lib/*.sh scripts/*.sh tests/lib/*.bash tests/fixtures/fake-go .envrc
claude plugin validate --strict .           # plugin manifest と hooks.json
claude plugin validate --strict skills      # skill の frontmatter
claude plugin test .                        # mod (hooks/*.test.tsx) のテスト
```

`invariants_test.go` と `fuzz_test.go` は「どの入力でも破ってはいけないこと」を書いた実行できる仕様。
反例が出たらコードを直すか挙動を正当化し、性質を弱めて通さない。

## mod とバイナリの約束

mod が呼ぶコマンドは入力を stdin の JSON で受け、出力を JSON で返す。形は `protocol.schema.json` の `$defs` が
1 箇所で持ち、Go の型と mod の型はどちらも schema から生成する。schema を変えたら `task protocol:generate` を打ち、
食い違いは `go build` と tsc に拾わせる。バイナリは入力を schema で確かめてから読む。
`ParkingItem` と `Edge` は state に書くファイル (`parking/`、`edges/`) の形でもあるので、schema を変えると
書き済みのファイルの読み方も変わる。

バイナリの置き場 (`${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`) は `hooks/lib/daemon-bin.sh`、
`hooks/daemon-bin.ts`、`Taskfile.yml` の `install` が同じ規則で持つ。変えるときは 3 つを揃える。

## 文章を書くときの決めごと

- 句読点は全角の「。」「、」を使う。半角の「｡」「､」は使わない (md、コメント、ログとエラーの文面のどれも)。
- 実装のパッケージは `internal/` からのパスではなくパッケージ名で指す (`detect`、`rexcli/blockstate`)。名前だけで
  一意に決まらないものは親を 1 つ添える (`daemon/ledger`、`deps/graph`)。ファイル名まで要るときはパッケージからの相対
  (`detect/real_sample_test.go`)。パスを書くのはこのファイルと `.go-arch-lint.yml` とコマンド
  (`go run ./cmd/agents-daemon`) だけ。

## hook を書くときの決めごと

- hook の stdout は Claude Code が読む。SessionStart の stdout は会話の context へ入るので、ログは stderr へ出す。
- SessionStart は 5 秒以内に返す。`go build` のような重い処理は `nohup ... </dev/null >>log 2>&1 &` で detach する。
- hook は `${CLAUDE_PLUGIN_ROOT}` に頼らず、自分の位置 (`$(dirname "$0")`) から plugin root を解決する。
  hooks.json では `"\"${CLAUDE_PLUGIN_ROOT}/hooks/...\""` と引用符で囲む (validate が未引用を警告する)。
- データを読む・加工する処理 (JSON) はバイナリのサブコマンドに持たせ、hook や statusline は stdin を渡すだけにする
  (`ingest-statusline`)。bash 3.2 と jq で timestamp や大きなファイルを扱わない。
- hook から多重化ツールを叩かない。hook は Claude Code の PATH を継承し、非対話で起動されたセッションでは
  homebrew のパスが載らない。多重化ツールへの操作は daemon に持たせる。
- Stop hook で transcript のターンの最後の応答を読まない。transcript は非同期に書かれ、Stop の時点では
  最後の応答を含まないことがある (Claude Code の hooks のドキュメント)。

## skill を書くときの決めごと

- script は `"${CLAUDE_PLUGIN_ROOT}/scripts/..."` と書く。置換されるのは Claude が読む本文だけで、
  Bash の環境変数としては export されない。frontmatter で置換されるかは確かめていないので、
  `allowed-tools` にパスを書かない。
- skill 間の参照とスラッシュコマンドは名前空間付きで書く (`agents-daemon:setup`、
  `/agents-daemon:setup`)。bare 名は `~/.claude/skills/` に残った旧コピーへ当たる。
- skill 内の references は相対リンクで書き、読む手順は `Read` で書く。
- この repo を保守する手順 (利用者の運用ではなく maintainer だけが使うもの) の skill は `.claude/skills/` に置き、
  plugin の `skills/` には入れない。`skills/` に置くと install した全セッションに description が載り、
  validate と tagpr のリリースにも乗る。名前には作業の性質 (追従、changelog など) を入れる。
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
- 削除は daemon 自身が mtime で行う (`internal/runtime/daemon/housekeeping.go`)。保持期間は定数にし、根拠をコメントに書く。

## リリース

tagpr (`.tagpr`、`.github/workflows/tagpr.yaml`) がリリース PR を作る。`version` は手で上げない。
tag に **`v` を付けない。** `v2026.0926.0` は Go module が major 2026 の semver と解釈して拒否する。

リリース PR の merge 後の plugin 更新と動いている daemon の入れ替えは、`agents-daemon update` が 1 回で行う。
sandbox が `~/.claude/plugins` と `~/.cache/agents-daemon` への書き込みを止めるので、利用者が `!` で打つ。
コマンドは `skills/agents-daemon/references/operations.md` の「plugin の更新後に daemon を入れ替える」。
