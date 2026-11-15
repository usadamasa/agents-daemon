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
| 止まったセッションへの働きかけ | 利用上限 (5 時間ウィンドウと、解除が 24 時間以内の週のウィンドウ) で止まったセッションを、transcript の末尾の entry から見つけて解除時刻に再開させる (`monitor`) |
| セッションを跨いだ状態の集約 | statusline の stdin から利用上限・context・cost をセッション別の state file に落とし、daemon とコマンドが読む (`sessionstate`、`ratelimit`、`sessioncontext`、`statusline/spend`)。Stop のたびに transcript の要約を集め、記憶の consolidation の材料にする (`dream`) |
| 時間で動く掃除 | ログと state file のうち、保持期間を過ぎたものを消す (`daemon/housekeeping.go`)。foreground セッションが keep したまま 1 週間触っていない worktree を、SessionStart のたびに今開いたリポジトリの分だけ回収する (`worktree`) |
| 多重化ツールへの操作 | rex の block を列挙し、画面を読み、キーと文面を送る (`rexcli`) |

境界は 1 つ。**Claude Code 本体が自分でできることは本体に任せる**。auto-continue が再開を予約している間は
手を出さず、本体の auto-continue が降りた場面だけを拾う。context の compact と prompt cache の失効も本体
(autocompact、idle compaction、失効後の footer の案内) に任せる。本体が同じことをするようになった機能は、
この境界に従って外す。

plugin が持つもの:

| 部品 | 役目 |
| ---- | ---- |
| SessionStart hook | daemon のバイナリを用意し、`daemon --ensure` で起こす。続けて `prune-worktrees --hook` で放置 worktree を回収する。resume なら作業の記録の終了を `ingest-work` で消す |
| Stop hook | `dream harvest` を起こす。turn の失敗の記録を `ingest-turn-failure` で消す。最後の応答の 1 行目を作業の記録の要約として `ingest-work` で書く |
| StopFailure / UserPromptSubmit hook | turn を終わらせた API error を `ingest-turn-failure` で記録し、次の turn で消す (#77)。UserPromptSubmit は最初の prompt を作業の記録の題として `ingest-work` で書く |
| SessionEnd hook | 作業の記録に終了の時刻と理由を `ingest-work` で書く |
| PostToolUse hook | Task 系ツール (TaskCreate / TaskUpdate / TaskList) と TodoWrite の呼び出しを `ingest-task` に渡し、セッションのタスクを記録する (#121) |
| Task 系ツールの配置 (mod、`hooks/task-tools.ts`) | TaskCreate と TaskUpdate を ToolSearch の裏から prompt の一覧へ置く (#499) |
| `agents-daemon:agents-daemon` skill | 運用と切り分けの手引き (症状から引く) |
| `agents-daemon:setup` skill | 前提の確認と statusline の配線、peer messaging の切り分け |
| `agents-daemon:peer-sessions` skill | 別セッション (peer) へ作業を渡す手順と、rex の block 操作 |
| `agents-daemon:dream` skill | 記憶階層の consolidation の手引き |
| `agents-daemon statusline` | `statusLine.command` に向ける statusline。描画と、daemon が読む state file の書き込みを同じプロセスで行う |
| `agents-daemon ingest-statusline` | 自前の statusline から stdin を渡して state file だけを書かせるサブコマンド |
| `agents-daemon dream` | consolidation の実行系 (`harvest` / `mine` / `stage` / `adopt` / `forget`) |
| `agents-daemon prune-worktrees` | 誰も使っていない Claude Code の worktree の回収 (SessionStart hook が `--hook` 付きで呼ぶ。手で叩く形は `skills/agents-daemon/references/operations.md`) |
| `agent-tasks` (macOS のアプリ、`gui/`) | 依存図 (#134): セッションをまたぐタスクの依存を、セッションの外の窓に箱と線で描く操作盤。生きているセッションとタスクをすべて描き、パーキングロットの一覧を下に出す。タスクやメモを選ぶとその場に単体ビューが開き、依存の付け外し、そのタスクのセッションへの指示、渡す・着手・完了・削除の依頼、メモを生きているセッションか新しいセッションへ渡す、捨てる、終了したセッションのタスクと作業を外す・メモにする、作業の題を直す、を行う (#364) |
| `agents-daemon deps` | アプリと mod が読む依存図の JSON (タスクの記録、作業の記録、status.json、辺、メモを組み直したもの) を出す。タスクの無いセッションは作業の記録から作業のノードになり、終了しても残る (#368) |
| `agents-daemon tell` | 生きているセッションの pane の入力欄に文面を打って Enter を送る。アプリの送達の経路 |
| `agents-daemon park hand` | パーキングのメモを取って生きているセッション (`tell`) か新しく起こすセッション (`spawn`) へ渡し、届かなければ戻す |
| `agents-daemon spawned-session mark` | `spawn` と peer-sessions skill が起こした rex session の中で、claude を起こす前に印を `spawned-sessions/` に書く。daemon が印を回り、pane が無くなった session を閉じる (#557) |
| `agents-daemon spawned-session close <session_id>` | peer を起こした側が、控えた session_id で peer の rex session を閉じて印を消す。rex にもう無い session は印の有無にかかわらず成功、印の無い生きた session は拒否する (#585、#596) |
| `agents-daemon ingest-task` | PostToolUse hook の stdin からタスクを `tasks/<session_id>/` に記録する |
| `agents-daemon ingest-work` | SessionStart / UserPromptSubmit / Stop / SessionEnd hook の stdin からセッションの作業 (最初の prompt の題、最後の応答の要約、turns、始まりと終わり) を `work/<session_id>.json` に記録する (#368) |
| `agents-daemon work set\|get` | 作業の題を書き換える (人が直した題、companion の推定の題)・記録を読む |
| `agents-daemon ingest-turn-failure` | hook の stdin から turn の失敗を `turn-failures/<session_id>.json` に記録し、消す |
| `agents-daemon park add\|set\|list\|discard\|withdraw\|adopt` | パーキングロット (タスクの途中で見つけた脇のメモ) を `parking/` に置く・status を書き換える・一覧する・古い open のメモをまとめて捨てる・置いたセッションが取り下げる・終了したセッションのタスクをメモへ移す。同じメモは 1 つにまとめる (#121) |
| `agents-daemon edge add\|rm\|list` | 作業を渡す側が、セッションをまたぐ作業の依存 (辺) を `edges/` に書く・消す・一覧する (#134。使い方は `skills/agents-daemon/references/operations.md`) |
| `park` tool (mod、`hooks/park.tsx`) | model が聞かずに脇のことをメモとしてパーキングロットへ置く tool (#121) |
| ファイルのメモの通知 (mod、`hooks/notice.ts`) | model が Read / Edit / Write で触ったファイルを files に持つ、ほかのセッションが置いたメモを、その tool の結果に添えて model へ 1 回だけ伝える (#89) |
| 渡した宛先の辺の確かめ (mod、`hooks/handoff-notice.ts`) | model が `SendMessage` で送った生きているセッションに依存の辺が無ければ、その tool の結果に辺の書き方を添えて model へ伝える (#172) |
| 作業の題の推定 (mod、`hooks/work-infer.ts`) | ターンを重ねたセッション (3 ターン以上) の会話から、作業の題を `$.model.fork` で 1 行に推定して作業の記録に添える (#368)。アプリでは「会話から自動で付けた題 · 直す」の chip で分かり、押すと直せる。人が直した題は上書きしない |

## 前提

- **rex**: daemon は rex の CLI (`rex ls` / `block call` / `capture` / `send`) で block を見て送信する。
  rex の外で動く Claude Code には何も届かず、`REX_SESSION` の無いセッションの SessionStart では daemon を起こさない
  (statusline は rex の外でも動く)。server の socket は `~/Library/Application Support/rex/server.sock`。
  sandbox の中から rex を叩くときの設定は「利用者の設定」にある。
  確かめた rex と Claude Code のバージョンは、前提を置いた箇所ごとの `rex-premise:` / `cc-premise:` の marker が持つ。
  marker の書き方は AGENTS.md の「Claude Code の挙動を書くときの決めごと」にある。
- **statusline の配線**: 使用率と解除時刻は statusline の stdin にしか来ない。
  `statusLine.command` をこの plugin のバイナリの `statusline` に向けるか、自前の statusline から
  `ingest-statusline` へ stdin を渡す 1 行を足す (下の「セットアップ」で行う)。
  block と session の対応も statusline が書く記録から引くので、必須になる。
- **Go toolchain**: SessionStart hook が plugin のソースから `go build` でバイナリを建てる。
  hook は Claude Code を起動したシェルの PATH で `go` を探す。無ければ `brew install go` などで入れる。
  初回の build では Go module のダウンロードにネットワークを使う。PATH の `go` が `go.mod` の `go` 行
  (1.27.4) より古ければ、Claude Code を開いたリポジトリの `GOTOOLCHAIN` (devShell の `local` など) にかかわらず、
  go がそのバージョンの toolchain を取得してビルドする。
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
build が失敗したら (toolchain を取得できない、など)、次のセッションの開始時に原因と建て直すコマンドがメッセージに出る。
セッションを開き直さずに建て直す手順は `skills/agents-daemon/references/operations.md` の
「build の失敗から建て直す」にある。

## セットアップ

install 後に rex の block の中で Claude Code を起動し、`/agents-daemon:setup` を実行する。
前提がそろっているかを確かめ、statusline への配線が無ければ足す案を出す。

## 利用者の設定

この plugin が利用者から受け取る値と、利用者が Claude Code の settings へ足す設定の一覧。
利用者固有の値 (PII の規則、skill の置き場や名前) は、ここに並べた口から渡す。

### config.json

`${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`。ファイルが無ければ全て既定値で動く。
daemon の設定キーは `skills/agents-daemon/references/operations.md` の「設定」にある。
daemon は毎 tick 読み直すので、編集は数秒で反映される。

dream が読むキーは `dream.policy` だけ。dream の規約のファイルのパスを配列で並べる。

```json
{"dream": {"policy": ["~/notes/dream-policy.json", "~/work/dream-policy.json"]}}
```

- `~/` は home、相対パスは config.json のディレクトリを起点にする。
- 個人の規約と仕事用の規約のように、別の場所のファイルを並べてよい。各ファイルの配列のフィールド (`pii`、`skillDirs` など) を
  並べた順につなぐ。PII の規則もこの順に当てる。`version` は各ファイルで `1` にする。
- キーが無ければ、dream は規約なしで動く。PII は組み込みの規則 (email、日本の携帯電話番号) だけを伏せ、
  skill の走査は `~/.claude/skills` と project の `.claude/skills` だけ、skill 名の接頭辞は外さない。
- キーがあるのに config.json か規約のファイルが読めない・壊れている・未知のフィールドがあると、
  `dream harvest` と `dream mine` はエラーで止まる。PII の規則が外れたまま記録を書かないため。
  harvest は Stop hook から裏で動くので、止まったことは `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/logs/dream-harvest.log` と
  `doctor` の「dream の規約」の節にだけ出る。
  規約を書いた・直したら `agents-daemon dream policy` で読めるかを確かめる (合わせた結果を JSON で出す)。

### dream の規約のファイル

JSON。`version` だけが必須で、ほかのフィールドは省いてよい。未知のフィールドはエラーにする。

```json
{
  "version": 1,
  "pii": [{"name": "member-id", "pattern": "\\bMEM-\\d{5}\\b", "replacement": "[MEMBER-ID]"}],
  "skillNamePrefixes": ["team"],
  "skillDirs": ["~/src/example/skills"],
  "delegates": [{"topic": "settings と hook", "skill": "example:config-management"}],
  "skillGuides": [{"scope": "仕事用の skill", "skill": "example:skill-guide"}],
  "memoryHomes": [{"when": "社外に出せない情報を含む", "home": "非公開リポジトリの skill"}]
}
```

| フィールド | 読み手 | 意味 |
| ---------- | ------ | ---- |
| `version` | dream | `1` |
| `pii` | `dream harvest` | 記録へ残す前に伏せる規則。`pattern` は Go の正規表現、`replacement` は展開せずそのまま置く。組み込みの規則より先に当てる |
| `skillNamePrefixes` | `dream mine` | memory 名との照合で skill 名から外す先頭の語。`team` なら、memory 名の `notes` が skill の `team-notes` に一致する |
| `skillDirs` | `dream mine` | `~/.claude/skills` の後、project の `.claude/skills` の前に走査する skill の置き場。相対パスは規約のファイルのディレクトリが起点 |
| `delegates` | dream skill | dream の対象外の話題と、それを担う skill |
| `skillGuides` | dream skill | skill を書くときに従う規範の skill (範囲ごと) |
| `memoryHomes` | dream skill | memory の行き先の規則。references の既定の表より先に当てる |

### settings の env

Claude Code の settings の `env` に置く。この plugin は既定値を持たず、無いときの挙動は表のとおり。

| 変数 | 読み手 | 無いとき |
| ---- | ------ | -------- |
| `CLAUDE_CODE_SUBAGENT_MODEL` | `spawn` と peer-sessions skill (起こすセッションの model) | `spawn` がセッションを起こさない |
| `CLAUDE_STATUSLINE_SPEND_CAP` | `statusline` (月額上限に対する消費。USD の整数) | 消費の表示を出さない |
| `CLAUDE_CODE_ENABLE_TODO_TOOLS` | Claude Code (Task 系ツール。依存図のタスクの元) | 依存図にタスクが載らない |

ほかに読む環境変数を並べる。どれも無ければ既定の置き場を使う。

- `CLAUDE_CONFIG_DIR`: Claude Code の設定ディレクトリ
- `REX_BIN_PATH`: rex のバイナリ。無ければ PATH から探す
- `XDG_CONFIG_HOME` / `XDG_STATE_HOME` / `XDG_CACHE_HOME`: 設定・state・バイナリの置き場

### sandbox

sandbox の中で使うときに settings へ足すもの。行の書き方は setup skill の references にある。

| 設定 | 値 | 要る場面 | 書き方 |
| ---- | -- | -------- | ------ |
| `sandbox.network.allowUnixSockets` | `~/Library/Application Support/rex` | `doctor` や setup skill を sandbox の中から打つ | `skills/setup/references/rex.md` |
| `sandbox.network.allowUnixSockets` | `/tmp/cc-socks`、`/private/tmp/cc-socks` | peer messaging (`ListAgents` / `SendMessage`) | `skills/setup/references/peer-messaging.md` |
| `sandbox.excludedCommands` | `~/.cache/agents-daemon/bin/agents-daemon dream forget *` | `dream forget` で auto memory を消す | `skills/setup/references/dream-forget.md` |

## コマンド

コマンドの一覧と呼び方は `skills/agents-daemon/references/operations.md` の「コマンド」にある。

## 依存図のアプリ (agent-tasks)

`gui/` は依存図をセッションの外の窓で描く macOS のアプリで、[Wails v3](https://v3.wails.io/) の WKWebView に
HTML の箱と SVG の線で描く。root とは別の Go module だが、daemon のバイナリと同じく SessionStart hook が plugin の
ソースから建てて、`~/Applications/agent-tasks.app` に置く (ad hoc 署名。手元で建てるので Gatekeeper は見ない)。
要るのは Go toolchain と Xcode Command Line Tools (`xcode-select --install`) で、無い Mac と macOS 以外では建てずに
`build.log` に 1 行残す。建て直しは plugin の更新後の次の SessionStart か `agents-daemon update`。
建てた plugin のバージョンは窓の title とアプリのメニューの About に出る (`task gui:build` で建てたものは `dev`)。
開いているアプリは、建て直されるとヘッダーに切り替えのボタンを出し、押すと窓と画面の状態を引き継いで新しいバージョンへ
入れ替わる (`dev` は出さない)。

対話のセッションが始まると、アプリを前面を奪わずに開く (開いていれば何もしない)。閉じた後は `/agent-tasks` で
前面に開き直す。起動時に開かないようにするには、環境変数 `AGENTS_DAEMON_OPEN_APP=0` を置く (settings.json の `env`)。
アプリがまだ建っていない最初のセッションでは開けず、次のセッションから開く。

アプリの中では英数キーの長押しがアクセント記号の候補ではなく文字の繰り返しになる。候補を出したいときは
`defaults write com.github.usadamasa.agents-daemon.agent-tasks ApplePressAndHoldEnabled -bool true` で戻す。

```sh
task gui:build    # bin/agent-tasks にビルドする (開発用。Xcode Command Line Tools が要る)
task gui:test     # go vet、gosec、go test と、フロントエンドの node --test (npm が要る)
task gui:install  # hook と同じ script で cache と ~/Applications/agent-tasks.app に置く
task gui:vendor   # 配置の library (dagre) を npm から gui/frontend/src/vendor に写す (版を上げたとき)
```

アプリは daemon のバイナリ (`${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`) のサブコマンドを呼ぶだけで、
状態はバイナリが持つ。数秒おきに `deps` を読み直し、操作は `park` / `edge` / `task dismiss` / `work set` / `spawn` / `tell` /
`park hand` に stdin の JSON で渡す。ログに添える操作主体は `app`。

セッションへ届ける操作 (指示、タスクの着手・完了・削除・渡す依頼、メモの手渡し) は `tell` が rex の pane の入力欄に
文面を打って Enter を送る形で届く。利用者の答えを待つ prompt が開いている、上限の画面、入力欄に打ちかけの文がある
pane には送らず、理由を文面の下に出す。アプリは rex の外で起きるので、新しいセッションを起こす rex の session は
利用者が最後に使っていた session (statusline の記録) になる。

## 開発

```sh
task build      # ./bin/ へビルド
task test       # go test と bats tests/
task lint       # Go の静的解析 (devShell のツールを使う)
task vulncheck  # govulncheck の symbol モード (lint が呼び、vulncheck workflow が週次で回す)
task install    # hook と同じ cache のパスへビルドする
claude plugin validate --strict .
```

開発には flakes を有効にした [Nix](https://nixos.org/) と、[direnv](https://direnv.net/) と
[nix-direnv](https://github.com/nix-community/nix-direnv) を使う。Go・task・golangci-lint・shellcheck・bats・jq・nodejs などは
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
