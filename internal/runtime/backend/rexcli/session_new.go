package rexcli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/api"
)

// selfBinary は包みが印を書くのに呼ぶ agents-daemon のパス。spawn を走らせている自分 (cache のバイナリ) を指す。
// テストは差し替える (go test のバイナリを包みから起こさない)。
var selfBinary = os.Executable

// sessionNewResult は session.create の応答。initial_windows[0].block_ids[0] が新しい session の pane。
// rex-premise: preview.30 api session.create は session_id と initial_windows (window_id、block_ids) を返す
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
//   - set -m で、起こすコマンドを自分のプロセスグループで端末の前景にする。sh と同じグループのままだと rex は sh を
//     前景と見て、claude が立てた端末の題の持ち主を sh とし、サイドバーに題を出さない (#570)。
//   - 起こす前に session の印を書く。後から開いた pane が残って自分では閉じられなかった session を、pane が空に
//     なった後で daemon が閉じる (#557)。印を書けなくてもコマンドは起こす (失敗は画面に出る)。
//   - 0 以外で終わったときは終わり方を出して Enter を待ち、それまで失敗の画面を残す。待つ間の Ctrl-C は Enter と同じ。
//   - 後から開いた pane やタブがあると session kill が確認なしで巻き込むので、そのときは閉じない (自分の pane だけ閉じる)。
//   - block ls か jq が失敗したときも閉じない側へ倒す。
//
// REX_SESSION は ID なので、起きた claude が session を rename しても当たる。peer-sessions skill の references/rex.md が
// 同じ本文を single quote で囲んで載せるので、本文に single quote を入れない。
// rex-premise: preview.30 cli block ls -s は session の全 window の block を返す。中のコマンドの終了で session は閉じない
// rex-premise: preview.28 program_status サイドバーは端末の題の持ち主 (block call title の owner) が前景のプロセスのときだけ題を出す
// macOS の /bin/sh (bash 3.2) は INT の trap の後も read を続けるので、閉じる処理 (c) を trap から直に呼んで抜ける。
const closeOnExitScript = `set -m; rex="$1"; ad="$2"; shift 2; "$ad" spawned-session mark "$REX_SESSION" >/dev/null; "$@"; s=$?; ` +
	`c() { if "$rex" --autostart=false block ls -s "$REX_SESSION" --json | ` +
	`jq -e --arg b "$REX_BLOCK" ".blocks | length == 1 and .[0].block_id == \$b" >/dev/null; ` +
	`then "$rex" --autostart=false session kill "$REX_SESSION"; fi; exit "$s"; }; ` +
	`if [ "$s" -ne 0 ]; then trap c INT; printf "\n%s が exit %s で終わった。Enter で閉じる。\n" "$1" "$s"; read -r _; fi; c`

// NewSession は session.create で新しい rex session を作り、その最初の pane の ID を返す (spawn.Host)。label は session と
// window の名前。argv は closeOnExitScript で包み、終わったら session を閉じる。異常終了では Enter まで画面を残す
// (起動の失敗を画面で読める) ので、exit は既定のまま (終わった block を残さない) にする。
// shell と login も既定のまま (login shell の環境で command を 1 引数ずつ子へ渡す) にし、CLI の session new と同じ環境と
// 引数で起こす (2026-11-17 に PATH と空白・`$` を含む引数が CLI と同じことを実機で確かめた)。起きたコマンドの
// REX_SESSION と REX_BLOCK は新しい session と block になる。label の先頭の `-` は外す (CLI が flag と読んで拒んだ名残で、
// 名前を揃える)。外して空なら label を付けない (rex の既定の名前になる)。
// 包みに渡す rex のパスは CLI の Runner が引く (包みの中は rex の CLI を叩く)。
func (c *execClient) NewSession(ctx context.Context, label, cwd string, argv []string) (string, error) {
	label = strings.TrimLeft(label, "-")
	bin, err := c.cli.Bin()
	if err != nil {
		return "", fmt.Errorf("rex の session を作れない: 包みに渡す rex が分からない: %w", err)
	}
	self, err := selfBinary()
	if err != nil {
		return "", fmt.Errorf("rex の session を作れない: 印を書く agents-daemon のパスが分からない: %w", err)
	}
	command := append([]string{"/bin/sh", "-c", closeOnExitScript, "agents-daemon-spawn", bin, self}, argv...)
	window := map[string]any{"layout": api.ShellLayout("", map[string]any{"cwd": cwd, "command": command})}
	req := map[string]any{"initial_windows": []any{window}}
	if label != "" {
		req["label"] = label
		window["window_label"] = label
	}
	var res sessionNewResult
	if err := c.rex.Call(ctx, "session.create", req, &res); err != nil {
		return "", err
	}
	if len(res.InitialWindows) == 0 || len(res.InitialWindows[0].BlockIDs) == 0 {
		return "", fmt.Errorf("rex の session.create の応答の block_ids が空で、起こした pane が分からない (session_id=%q)", res.SessionID)
	}
	block := res.InitialWindows[0].BlockIDs[0]
	_ = c.rex.Release(ctx, res.SessionID, []string{block}) // 手放せなくても pane は動いている (api.Client.Release)
	return block, nil
}
