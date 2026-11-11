package rexcli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// selfBinary は包みが印を書くのに呼ぶ agents-daemon のパス。spawn を走らせている自分 (cache のバイナリ) を指す。
// テストは差し替える (go test のバイナリを包みから起こさない)。
var selfBinary = os.Executable

// sessionNewResult は `session new --json` の応答。initial_windows[0].block_ids[0] が新しい session の pane。
// rex-premise: preview.25 cli session new --json は session_id と initial_windows (window_id、block_ids) を返す
type sessionNewResult struct {
	SessionID      string `json:"session_id"`
	InitialWindows []struct {
		WindowID string   `json:"window_id"`
		BlockIDs []string `json:"block_ids"`
	} `json:"initial_windows"`
}

// closeOnExitScript は起こしたコマンドを走らせ、終わったら session に残る block が自分 (REX_BLOCK) だけのときに
// 自分の rex session を閉じる sh の本文。$1 が rex のバイナリ、$2 が agents-daemon のバイナリ、残りが起こす argv。
// rex は中のコマンドが終わっても session を閉じず、pane が閉じても window が 0 の session が残る (#391)。
//   - 起こす前に session の印を書く。後から開いた pane が残って自分では閉じられなかった session を、pane が空に
//     なった後で daemon が閉じる (#557)。印を書けなくてもコマンドは起こす (失敗は画面に出る)。
//   - 0 以外で終わったときは終わり方を出して Enter を待ち、それまで失敗の画面を残す。待つ間の Ctrl-C は Enter と同じ。
//   - 後から開いた pane やタブがあると session kill が確認なしで巻き込むので、そのときは閉じない (自分の pane だけ閉じる)。
//   - block ls か jq が失敗したときも閉じない側へ倒す。
//
// REX_SESSION は ID なので、起きた claude が session を rename しても当たる。peer-sessions skill の references/rex.md が
// 同じ本文を single quote で囲んで載せるので、本文に single quote を入れない。
// rex-premise: preview.28 cli block ls -s は session の全 window の block を返す。中のコマンドの終了で session は閉じない
// macOS の /bin/sh (bash 3.2) は INT の trap の後も read を続けるので、閉じる処理 (c) を trap から直に呼んで抜ける。
const closeOnExitScript = `rex="$1"; ad="$2"; shift 2; "$ad" spawned-session mark "$REX_SESSION" >/dev/null; "$@"; s=$?; ` +
	`c() { if "$rex" --autostart=false block ls -s "$REX_SESSION" --json | ` +
	`jq -e --arg b "$REX_BLOCK" ".blocks | length == 1 and .[0].block_id == \$b" >/dev/null; ` +
	`then "$rex" --autostart=false session kill "$REX_SESSION"; fi; exit "$s"; }; ` +
	`if [ "$s" -ne 0 ]; then trap c INT; printf "\n%s が exit %s で終わった。Enter で閉じる。\n" "$1" "$s"; read -r _; fi; c`

// NewSession は `session new` で新しい rex session を作り、その最初の pane の ID を返す (spawn.Host)。label は session と
// window の名前。argv は closeOnExitScript で包み、終わったら session を閉じる。異常終了では Enter まで画面を残す
// (起動の失敗を画面で読める) ので、`--keep-open` は付けない。付けると終わった block が入力を受けないまま残る。
// 起きたコマンドの REX_SESSION と REX_BLOCK は新しい session と block になる (実測)。argv は `--` の後にそのまま渡し、rex は login shell の環境で 1 引数ずつ子へ渡す
// (空白、`$` を変えない。実測)。LABEL は位置引数で、rex は `-` で始まる値を flag と読んで拒むので、先頭の `-` を外す。
// 外して空なら label を付けない (rex の既定の名前になる)。
// rex-premise: preview.26 cli session new は LABEL --cwd --window --json を取り、-- の後の argv をそのまま渡す
func (c *execClient) NewSession(ctx context.Context, label, cwd string, argv []string) (string, error) {
	label = strings.TrimLeft(label, "-")
	args := []string{"session", "new"}
	if label != "" {
		args = append(args, label)
	}
	args = append(args, "--cwd", cwd)
	if label != "" {
		args = append(args, "--window", label)
	}
	bin, err := c.bin()
	if err != nil {
		return "", fmt.Errorf("rex session new: %w", err)
	}
	self, err := selfBinary()
	if err != nil {
		return "", fmt.Errorf("rex session new: 印を書く agents-daemon のパスが分からない: %w", err)
	}
	args = append(args, "--json", "--", "/bin/sh", "-c", closeOnExitScript, "agents-daemon-spawn", bin, self)
	args = append(args, argv...)
	var res sessionNewResult
	if err := c.runJSON(ctx, &res, args...); err != nil {
		return "", err
	}
	if len(res.InitialWindows) == 0 || len(res.InitialWindows[0].BlockIDs) == 0 {
		return "", fmt.Errorf("rex session new: 応答の block_ids が空で、起こした pane が分からない (session_id=%q)", res.SessionID)
	}
	return res.InitialWindows[0].BlockIDs[0], nil
}
