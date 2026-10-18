# agents-daemon

Claude Code のセッション群のそばに常駐し、セッションの中からはできないことを背後で担うデーモンと、
それを配線する Claude Code plugin。端末多重化ツール rex の block 越しにセッションを見て、働きかける。

## 役割

Claude Code のセッションは自分が止まっている間は何もできず、自分以外のセッションの状態を知らず、
時間が経っても勝手には動かず、自分の pane の外の端末を触れない。agents-daemon はセッションの外に
1 プロセスだけ常駐し、この「中からはできないこと」を引き受ける。機能を足すときは下の表の区分のどれかに
属するかで判断する。

| 区分 | 今ある機能 |
| ---- | ---- |
| 止まったセッションへの働きかけ | 5 時間ウィンドウの利用上限で止まったセッションを、解除時刻に再開させる (`internal/monitor`) |
| セッションを跨いだ状態の集約 | statusline の stdin から利用上限・context・cost をセッション別の state file に落とし、daemon とコマンドが読む (`internal/sessionstate`、`internal/ratelimit`、`internal/sessioncontext`、`internal/spend`)。Stop のたびに transcript の要約を集め、記憶の consolidation の材料にする (`internal/dream`) |
| 時間で動く掃除 | ログと state file のうち、保持期間を過ぎたものを消す (`internal/daemon/housekeeping.go`)。foreground セッションが keep したまま 1 週間触っていない worktree を、SessionStart のたびに今開いたリポジトリの分だけ回収する (`internal/worktree`) |
| 多重化ツールへの操作 | rex の block を列挙し、画面を読み、キーと文面を送る (`internal/rexcli`) |

境界は 1 つ。**Claude Code 本体が自分でできることは本体に任せる**。auto-continue が再開を予約している間は
手を出さず、本体の auto-continue が降りた場面だけを拾う。context の compact と prompt cache の失効も本体
(autocompact、idle compaction、失効後の footer の案内) に任せる。以前この plugin が持っていた compact の
自動化と ttl-guard は、本体が同じことをするようになった時点でこの境界に従って外した (#59)。

plugin が持つもの:

| 部品 | 役目 |
| ---- | ---- |
| SessionStart hook | daemon のバイナリを用意し、`daemon --ensure` で起こす。続けて `prune-worktrees --hook` で放置 worktree を回収する |
| Stop hook | `dream harvest` を起こす |
| PostToolUse hook | Task 系ツール (TaskCreate / TaskUpdate / TaskList) と TodoWrite の呼び出しを `ingest-task` に渡し、セッションのタスクを記録する (#121) |
| `agents-daemon:agents-daemon` skill | 運用と切り分けの手引き (症状から引く) |
| `agents-daemon:setup` skill | 前提の確認と statusline の配線、peer messaging の切り分け |
| `agents-daemon:peer-sessions` skill | 別セッション (peer) へ作業を渡す手順と、rex の block 操作 |
| `agents-daemon:dream` skill | 記憶階層の consolidation の手引き |
| `agents-daemon statusline` | `statusLine.command` に向ける statusline。描画と、daemon が読む state file の書き込みを同じプロセスで行う |
| `agents-daemon ingest-statusline` | 自前の statusline から stdin を渡して state file だけを書かせるサブコマンド |
| `agents-daemon dream` | consolidation の実行系 (`harvest` / `mine` / `stage` / `adopt` / `forget`) |
| `agents-daemon prune-worktrees` | 誰も使っていない Claude Code の worktree の回収 (SessionStart hook が `--hook` 付きで呼ぶ。手で叩く形は `skills/agents-daemon/references/operations.md`) |
| `board` コマンド (mod、`hooks/board.tsx`) | Claude Code の mods の pane にタスクのボードを描く。行は repo (worktree は同じ行)、列は未着手・進行中・ブロック・完了。入力待ちや上限待ちのセッションは進行中のカードに印が付く。開いたセッションの domain (私用か業務) だけを描き、カードを選ぶとそのタスクのセッションへ指示を送れる。描画されるのは terminal 2.1.287 以降と Desktop の Code タブ 2.1.286 以降 (#121) |
| `agents-daemon status --board` | mod が読むボードの JSON (タスクの記録と status.json を組み直したもの) を出す |
| `agents-daemon ingest-task` | PostToolUse hook の stdin からタスクを `tasks/<domain>/` に記録する。私用 (`home`) と業務 (`work`) は config の `homePaths` / `defaultDomain` で分ける |
| `agents-daemon park add\|set\|list` | パーキングロット (タスクの途中で見つけた脇の件) を `parking/<domain>/` に置く・status を書き換える・一覧する。同じ件は 1 件にまとめる (#121) |
| `park` tool (mod、`hooks/park.tsx`) | model が聞かずに脇のことをメモとしてパーキングロットへ置く tool と、repo の open のメモの数を出す status 行 (#121) |

## 前提

- **rex**: daemon は rex の CLI (`rex ls` / `block call` / `capture` / `send`) で block を見て送信する。
  rex の外で動く Claude Code には何も届かず、`REX_SESSION` の無いセッションの SessionStart では daemon を起こさない
  (statusline は rex の外でも動く)。server の socket は `~/Library/Application Support/rex/server.sock`。
  sandbox の中の Bash から `doctor` や setup skill で rex を叩くなら、
  `sandbox.network.allowUnixSockets` に `~/Library/Application Support/rex` を足す。
  rex `0.0.1791570264-g7b5bf3` と Claude Code `2.1.296` で測った (2026-10-10)。
- **statusline の配線**: 使用率と解除時刻は statusline の stdin にしか来ない。
  `statusLine.command` をこの plugin のバイナリの `statusline` に向けるか、自前の statusline から
  `ingest-statusline` へ stdin を渡す 1 行を足す (下の「セットアップ」で行う)。
  block と session の対応も statusline が書く記録から引くので、必須になる。
- **Go toolchain**: SessionStart hook が plugin のソースから `go build` でバイナリを建てる。
  hook は Claude Code を起動したシェルの PATH で `go` を探す。無ければ `brew install go` などで入れる。
  初回の build では Go module のダウンロードにネットワークを使う。
- **jq**: hook が plugin.json の version と hook 入力の JSON を読むのに使う。

## インストール

```sh
claude plugin marketplace add usadamasa/agents-marketplace
claude plugin install agents-daemon@usadamasa
```

次のセッションの SessionStart で、hook が `go build` をバックグラウンドで回す。
セッションの開始は待たせず、build が終わると daemon が起きる。
バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` に置かれ、
plugin を更新すると次の SessionStart で建て直される。
動作中の daemon は実行ファイルの差し替えを検知して自分で入れ替わる。
セッションを開き直さずに入れ替える手順は `skills/agents-daemon/references/operations.md` の
「plugin の更新後に daemon を入れ替える」にある。

build の経過は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/logs/build.log` に残る。

## セットアップ

install 後に rex の block の中で Claude Code を起動し、`/agents-daemon:setup` を実行する。
前提がそろっているかを確かめ、statusline への配線が無ければ足す案を出す。

## 設定

`${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`。ファイルが無ければ全て既定値で動く。
daemon は毎 tick 読み直すので、編集は数秒で反映される。
設定キーの一覧は `skills/agents-daemon/references/operations.md` にある。

## コマンド

バイナリは PATH に入らないので、絶対パスで呼ぶかそのディレクトリを PATH に足す。

```sh
agents-daemon status                     # 稼働状況、監視中の pane、ログの末尾
agents-daemon logs -n 80                 # daemon.log の末尾
agents-daemon doctor                     # 前提条件の診断
agents-daemon inspect --pane <block id>  # 生きた block の画面を分類する
agents-daemon daemon --foreground --dry-run  # 送信せずに判定だけ回す
agents-daemon stop                       # 停止
COLUMNS=200 agents-daemon statusline < in.json  # statusline を描画し、state file も書く (statusLine.command が呼ぶ)
agents-daemon ingest-statusline < in.json  # statusline の stdin から state file だけを書く (自前の statusline が呼ぶ)
agents-daemon dream harvest --incremental  # 直近の transcript を集める (Stop hook が --detach 付きで呼ぶ)
agents-daemon dream mine --project <canonical>   # memory と skill を突き合わせて候補を出す
agents-daemon dream stage --project <canonical>  # 候補の承認レポートを ./tmp/dream/ に書く
agents-daemon dream forget <memory-name>...      # auto memory を消す (sandbox の外で動かす)
agents-daemon prune-worktrees --dry-run          # 回収対象の worktree を表示する (SessionStart hook が --hook 付きで呼ぶ)
```

## 開発

```sh
task build    # ./bin/ へビルド
task test     # go test と bats tests/
task lint     # Go の静的解析 (devShell のツールを使う)
task install  # hook と同じ cache のパスへビルドする
claude plugin validate --strict .
```

開発には flakes を有効にした [Nix](https://nixos.org/) と、[direnv](https://direnv.net/) と
[nix-direnv](https://github.com/nix-community/nix-direnv) を使う。Go・task・golangci-lint・shellcheck・bats・jq などは
`flake.nix` の devShell で揃え、版は `flake.lock` が固定する nixpkgs で決まる。`.envrc` が `use flake` で devShell を
読み込むので `direnv allow` する。CI も同じ flake を使う。plugin を使うだけなら Nix は要らない。

clone をそのまま読み込ませると、hook と skill の編集は install し直さずに反映される。
このリポジトリは marketplace.json を持たないので、`--plugin-dir` でセッション単位に読み込む。
install 済みの版と二重に hook が走らないよう、確認中は install 済みの plugin を無効にしておく。

```sh
claude plugin disable agents-daemon@usadamasa
claude --plugin-dir "$PWD"
```

## License

MIT
