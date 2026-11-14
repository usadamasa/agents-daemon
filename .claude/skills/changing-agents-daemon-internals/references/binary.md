# バイナリの用意と差し替え

plugin には install 時の lifecycle hook が無いので、バイナリも SessionStart hook が用意する。
置き場は `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon`。plugin の install 先はバージョンごとに
変わるため、その外の固定パスにする。

## build

| 状態 | `ensure-daemon.sh` の動き |
| ---- | ---- |
| `${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon` があり、隣の `agents-daemon.version` が plugin.json の `version` と一致 | `daemon --ensure` を exec する |
| それ以外 (未ビルド、plugin の更新後) | `hooks/build-daemon.sh` を detach して即座に戻る |

`build-daemon.sh` は plugin の root で `go build ./cmd/agents-daemon` を回し、同一ディレクトリの
一時ファイルから `mv` でバイナリを差し替え、stamp を書いてから `daemon --ensure` を叩く。
出力は `$STATE/logs/build.log` に残る。SessionStart を `go build` で待たせないため、
初回のセッションでは daemon が build の完了後に起きる。

- `go` が PATH に無ければ `build.log` に `ERROR ... go not found in PATH` を残して終わる。
  hook は PATH を仮定しないので、Claude Code を起動するシェルの PATH に Go を入れる。
- go build は呼び出しにだけ `GOTOOLCHAIN=auto` を渡す (`hooks/lib/go-build.sh`)。起動したリポジトリの devShell の
  `GOTOOLCHAIN=local` を受け継ぐと、PATH の go が `go.mod` より古いとき落ちるため。export しないので daemon には渡らない。
- go build が落ちたら、go の最後の行と `!` で打つ建て直しのコマンドを `$STATE/build.failed` (アプリは
  `build-gui.failed`) と `build.log` に書く。次の SessionStart で `ensure-daemon.sh` が、その成果物を建て直すときだけ
  原因の行を systemMessage に載せる (stamp が current なら載せない。`task install` や worktree の build が残した失敗印で、
  動いているものに失敗と出さないため)。建て直すコマンドは印の 2 行目ではなく hook 自身の置き場から組む
  (印は失敗した時点の置き場を指し、plugin の更新で消えうる)。建てられたら build の script が消す。
- 同時に起きた build は `bin/.build.lock` (ディレクトリ。中の `pid` が持ち主) で 1 本に絞る。
  持ち主が生きていれば後から来た側は何もせずに終わる。build が SIGKILL などで落ちて lock が残っても、
  次の build が持ち主の死を `kill -0` で確かめて残骸を消し、取り直す (`build.log` に `stale lock` が残る)。
  手で消す必要は無い。
- plugin の更新後にセッションを開き直さず入れ替えるには、`agents-daemon update` が plugin を更新してから
  更新した版の `hooks/build-daemon.sh` を叩く (`selfupdate`)。使い方は [operations.md](../../../../skills/agents-daemon/references/operations.md) の
  「plugin の更新後に daemon を入れ替える」。
- 開発中は `task install` で同じパスへ建てられる。中身は `hooks/build-daemon.sh` そのもので、
  stamp には clone の plugin.json の version が書かれる。install 済み plugin と version が同じなら
  次の SessionStart でも建て直されず、clone の版が動き続ける。
- worktree を `claude --plugin-dir <worktree>` で読み込んだセッションも同じパスを使う。stamp が無いか、
  worktree の plugin.json の version より古いと、SessionStart が worktree のコードで建て直す。
  バイナリは全セッションの mod と daemon が共有するので、CLI の受け方を変えた worktree のバイナリが
  install 済みの mod とかみ合わなくなる。state も共有なので、検証で置いたメモや辺は実際の依存図に入る。
- 検証の後は、検証で置いたメモと辺を捨て、`agents-daemon update` で install 済みのバージョンへ建て直す。
  update は installPath の `hooks/build-daemon.sh` を version を比べずに叩く。
  sandbox が `~/.cache/agents-daemon` への書き込みを止めるので、利用者が
  `! ~/.cache/agents-daemon/bin/agents-daemon update` と打つ。

## 動いている daemon の入れ替え

Go のバイナリは起動時にメモリへ読み込まれるので、ファイルを差し替えても
動作中のプロセスには反映されない。`--ensure` は「生きているデーモンがあれば何もしない」
ため、放っておくと直したはずのコードが何日も動かないままになる。

そこでデーモンは毎 tick で自分の実行ファイルの mtime とサイズを見て、差し替わっていたら
後始末をして `daemon.lock` を離してから新しい版を detach 起動して入れ替わる (`daemon/selfupdate.go`)。
build の数秒後には新しい版が動いている。hook も手動操作も停止・再起動をしない。
`build-daemon.sh` の `daemon --ensure` と入れ替わりの spawn が重なっても、lock を取れた 1 つだけが daemon になり、
負けた側は `daemon.log` に `agents-daemon: daemon は既に起動しています` の 1 行を残して終わる。

判定できないとき (実行ファイルが一時的に見えない等) は「変わっていない」に倒す。
動いているデーモンを不用意に落とす方が害が大きいため。
