# agents-daemon

herdr のペインで動く Claude Code セッションを見守る常駐デーモンと、それを配線する Claude Code plugin｡

## 何をするか

役目は 2 つある｡

- **利用上限からの自動再開**: 5 時間ウィンドウの利用上限で止まったセッションを、解除時刻に再開させる｡
  Claude Code 自身の auto-continue が再開を予約している間は手を出さず、ネイティブが降りた場面だけを拾う｡
- **context の自動 compact**: context 使用率が閾値を超えたら `/agents-daemon:compact-prep` で復旧材料を書かせ、
  続けて `/compact` を投入し、圧縮後に作業を再開させる (既定は off)｡

plugin が持つもの:

| 部品 | 役目 |
| ---- | ---- |
| SessionStart hook | daemon のバイナリを用意し、`daemon --ensure` で起こす |
| PostCompact hook | 圧縮完了の marker を置く |
| UserPromptSubmit hook | marker を見て、圧縮直後の 1 ターンだけ復旧ガイドを注入する |
| `agents-daemon:compact-prep` skill | `/compact` 前に作業状態を state file へ保存する |
| `agents-daemon:agents-daemon` skill | 運用と切り分けの手引き (症状から引く) |

## 前提

- **[herdr](https://herdr.dev)**: daemon は `herdr pane list` / `pane read` / `pane send-text` で pane を見て送信する｡
  herdr の外で動く Claude Code には何も届かない｡
- **statusline が state file を書くこと**: 使用率と解除時刻は statusline の stdin にしか来ない｡
  この plugin は statusline を含まないので、利用者の statusline が下の「statusline が書く state file」を書く｡
  書かれていなければ、上限側のゲートは働かず、compact の自動投入は動かない｡
- **Go toolchain**: SessionStart hook が plugin のソースから `go build` でバイナリを建てる｡
  hook は Claude Code を起動したシェルの PATH で `go` を探す｡無ければ `brew install go` などで入れる｡
  初回の build では Go module のダウンロードにネットワークを使う｡
- **jq**: hook が plugin.json の version と hook 入力の JSON を読むのに使う｡

## インストール

```sh
claude plugin marketplace add usadamasa/agents-marketplace
claude plugin install agents-daemon@agents-marketplace
```

次のセッションの SessionStart で、hook が `go build` をバックグラウンドで回す｡
セッションの開始は待たせず、build が終わると daemon が起きる｡
バイナリは `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` に置かれ、
plugin を更新すると次の SessionStart で建て直される｡動作中の daemon は実行ファイルの差し替えを検知して自分で入れ替わる｡

build の経過は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/logs/build.log` に残る｡

## 設定

`${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json`｡ファイルが無ければ全て既定値で動く｡
daemon は毎 tick 読み直すので、編集は数秒で反映される｡

context の自動 compact を使う最小例:

```json
{"compactAutoEnabled": true}
```

設定キーの一覧は `skills/agents-daemon/references/operations.md` にある｡

## statusline が書く state file

置き場は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/`｡どちらもセッションごとに別ファイルへ書き、
同一ディレクトリの一時ファイルへ書いてから `mv` で置き換える (daemon が書きかけを読まないため)｡
statusline の描画を壊さないよう、書き込みの失敗は握りつぶしてよい｡

`rate-limits/<session_id>.json`: stdin の `rate_limits.five_hour.resets_at` があるときだけ書く｡
null のとき (初回 API レスポンス前、ウィンドウ切り替えの瞬間) は書かず、既存ファイルも消さない｡

```json
{
  "five_hour": {"used_percentage": 42, "resets_at": 1786851000},
  "seven_day": {"used_percentage": 18, "resets_at": 1787300000},
  "observed_at": 1786840000,
  "session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b"
}
```

`context/<session_id>.json`: 描画のたびに必ず書く｡`used_percentage` は stdin の `context_window.used_percentage`｡

```json
{"session_id": "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b", "used_percentage": 63, "observed_at": 1786840000}
```

`used_percentage` は整数に丸め、`resets_at` と `observed_at` は Unix epoch 秒で書く｡
`seven_day` は入力にあるときだけ付ける｡

jq で組み立てる例:

```sh
dir="${XDG_STATE_HOME:-$HOME/.local/state}/agents-daemon/context"
mkdir -p "$dir" &&
  tmp=$(mktemp "$dir/.context.XXXXXX") &&
  jq -c '{session_id, used_percentage: ((.context_window.used_percentage // 0) | round), observed_at: (now | floor)}' \
    <<<"$input" >"$tmp" &&
  mv "$tmp" "$dir/$(jq -r .session_id <<<"$input").json"
```

## コマンド

バイナリは PATH に入らないので、絶対パスで呼ぶかそのディレクトリを PATH に足す｡

```sh
agents-daemon status                     # 稼働状況、監視中の pane、ログの末尾
agents-daemon logs -n 80                 # daemon.log の末尾
agents-daemon doctor                     # 前提条件の診断
agents-daemon inspect --pane <id>        # 生きた pane の画面を分類する
agents-daemon daemon --foreground --dry-run  # 送信せずに判定だけ回す
agents-daemon stop                       # 停止
```

## 旧 claude-auto-retry からの移行

1. 旧 daemon を止める (`claude-auto-retry stop`、または自己終了を待つ)｡
2. 旧 hook と旧 `compact-prep` skill を `~/.claude/` から外す｡同名の skill が `~/.claude/skills/` に残っていると、
   plugin の skill より先に当たる｡
3. `~/.claude/auto-retry.json` の中身を `${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json` へ移す｡
   `compactAutoPrepMessage` を明示していたら `/agents-daemon:compact-prep` に変える (既定値はすでにこれ)｡
4. statusline の書き込み先を `claude-auto-retry` から `agents-daemon` のディレクトリへ変える｡
5. `~/.local/state/claude-auto-retry/` は読まれない｡消してよい｡
6. plugin を install する (上の「インストール」)｡

## 開発

```sh
task build    # ./bin/ へビルド
task test     # go test と bats tests/
task lint     # Go の静的解析 (aqua のツールを使う)
task install  # hook と同じ cache のパスへビルドする
claude plugin validate --strict .
```

clone をそのまま読み込ませると、hook と skill の編集は install し直さずに反映される｡
このリポジトリは marketplace.json を持たないので、`--plugin-dir` でセッション単位に読み込む｡
install 済みの版と二重に hook が走らないよう、確認中は install 済みの plugin を無効にしておく｡

```sh
claude plugin disable agents-daemon@agents-marketplace
claude --plugin-dir "$PWD"
```

## License

MIT
