# agents-daemon

端末多重化ツール rex の block で動く Claude Code セッションを見守る常駐デーモンと､
それを配線する Claude Code plugin｡

## 何をするか

役目は 2 つある｡

- **利用上限からの自動再開**: 5 時間ウィンドウの利用上限で止まったセッションを､解除時刻に再開させる｡
  Claude Code 自身の auto-continue が再開を予約している間は手を出さず､ネイティブが降りた場面だけを拾う｡
- **context の自動 compact**: context 使用率が閾値を超えたら `/agents-daemon:compact-prep` で復旧材料を書かせ､
  続けて `/compact` を投入し､圧縮後に作業を再開させる (既定は off)｡

plugin が持つもの:

| 部品 | 役目 |
| ---- | ---- |
| SessionStart hook | daemon のバイナリを用意し､`daemon --ensure` で起こす |
| PostCompact hook | 圧縮完了の marker を置く |
| UserPromptSubmit hook | marker を見て､圧縮直後の 1 ターンだけ復旧ガイドを注入する｡cache 失効後の最初の prompt を止める (`ttl-guard`) |
| `agents-daemon:compact-prep` skill | `/compact` 前に作業状態を state file へ保存する |
| `agents-daemon:agents-daemon` skill | 運用と切り分けの手引き (症状から引く) |
| `agents-daemon:setup` skill | 前提の確認と statusline の配線､peer messaging の切り分け |
| `agents-daemon:peer-sessions` skill | 別セッション (peer) へ作業を渡す手順と､rex の block 操作 |
| `agents-daemon statusline` | `statusLine.command` に向ける statusline｡描画と､daemon が読む state file の書き込みを同じプロセスで行う |
| `agents-daemon ingest-statusline` | 自前の statusline から stdin を渡して state file だけを書かせるサブコマンド |

## 前提

- **rex**: daemon は rex の CLI (`rex ls` / `block call` / `capture` / `send`) で block を見て送信する｡
  rex の外で動く Claude Code には何も届かず､`REX_SESSION` の無いセッションの SessionStart では daemon を起こさない
  (statusline と `ttl-guard` は rex の外でも動く)｡server の socket は `~/Library/Application Support/rex/server.sock`｡
  sandbox の中の Bash から `doctor` や setup skill で rex を叩くなら､
  `sandbox.network.allowUnixSockets` に `~/Library/Application Support/rex` を足す｡
  rex `0.0.1791570264-g7b5bf3` と Claude Code `2.1.296` で測った (2026-10-10)｡
- **statusline の配線**: 使用率と解除時刻と transcript のパスは statusline の stdin にしか来ない｡
  `statusLine.command` をこの plugin のバイナリの `statusline` に向けるか､自前の statusline から
  `ingest-statusline` へ stdin を渡す 1 行を足す (下の「セットアップ」で行う)｡
  block と session の対応も statusline が書く記録から引くので､必須になる｡
- **Go toolchain**: SessionStart hook が plugin のソースから `go build` でバイナリを建てる｡
  hook は Claude Code を起動したシェルの PATH で `go` を探す｡無ければ `brew install go` などで入れる｡
  初回の build では Go module のダウンロードにネットワークを使う｡
- **jq**: hook が plugin.json の version と hook 入力の JSON を読むのに使う｡

## インストール

```sh
claude plugin marketplace add usadamasa/agents-marketplace
claude plugin install agents-daemon@usadamasa
```

次のセッションの SessionStart で､hook が `go build` をバックグラウンドで回す｡
セッションの開始は待たせず､build が終わると daemon が起きる｡
バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` に置かれ､
plugin を更新すると次の SessionStart で建て直される｡動作中の daemon は実行ファイルの差し替えを検知して自分で入れ替わる｡
セッションを開き直さずに入れ替える手順は `skills/agents-daemon/references/operations.md` の
「plugin の更新後に daemon を入れ替える」にある｡

build の経過は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/logs/build.log` に残る｡

## セットアップ

install 後に rex の block の中で Claude Code を起動し､`/agents-daemon:setup` を実行する｡
前提がそろっているかを確かめ､statusline への配線が無ければ足す案を出す｡

## 設定

`${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`｡ファイルが無ければ全て既定値で動く｡
daemon は毎 tick 読み直すので､編集は数秒で反映される｡

context の自動 compact を使う最小例:

```json
{"compactAutoEnabled": true}
```

設定キーの一覧は `skills/agents-daemon/references/operations.md` にある｡

## コマンド

バイナリは PATH に入らないので､絶対パスで呼ぶかそのディレクトリを PATH に足す｡

```sh
agents-daemon status                     # 稼働状況､監視中の pane､ログの末尾
agents-daemon logs -n 80                 # daemon.log の末尾
agents-daemon doctor                     # 前提条件の診断
agents-daemon inspect --pane <block id>  # 生きた block の画面を分類する
agents-daemon daemon --foreground --dry-run  # 送信せずに判定だけ回す
agents-daemon stop                       # 停止
COLUMNS=200 agents-daemon statusline < in.json  # statusline を描画し､state file も書く (statusLine.command が呼ぶ)
agents-daemon ingest-statusline < in.json  # statusline の stdin から state file だけを書く (自前の statusline が呼ぶ)
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
[nix-direnv](https://github.com/nix-community/nix-direnv) を使う。Go・task・golangci-lint・shellcheck・bats などは
`flake.nix` の devShell で揃え、版は `flake.lock` が固定する nixpkgs で決まる。`.envrc` が `use flake` で devShell を
読み込むので `direnv allow` する。CI も同じ flake を使う。plugin を使うだけなら Nix は要らない。

clone をそのまま読み込ませると､hook と skill の編集は install し直さずに反映される｡
このリポジトリは marketplace.json を持たないので､`--plugin-dir` でセッション単位に読み込む｡
install 済みの版と二重に hook が走らないよう､確認中は install 済みの plugin を無効にしておく｡

```sh
claude plugin disable agents-daemon@usadamasa
claude --plugin-dir "$PWD"
```

## License

MIT
