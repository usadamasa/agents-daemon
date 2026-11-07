package rexcli

import (
	"context"
	"fmt"
	"strings"
)

// sessionNewResult は `session new --json` の応答。initial_windows[0].block_ids[0] が新しい session の pane。
// rex-premise: preview.25 cli session new --json は session_id と initial_windows (window_id、block_ids) を返す
type sessionNewResult struct {
	SessionID      string `json:"session_id"`
	InitialWindows []struct {
		WindowID string   `json:"window_id"`
		BlockIDs []string `json:"block_ids"`
	} `json:"initial_windows"`
}

// NewSession は `session new` で新しい rex session を作り、その最初の pane の ID を返す (spawn.Host)。label は session と
// window の名前。`--keep-open` で終了後も block を残す (起動の失敗を画面で読める)。起きたコマンドの REX_SESSION は
// 新しい session になる (実測)。argv は `--` の後にそのまま渡し、rex は login shell の環境で 1 引数ずつ子へ渡す
// (空白、`$` を変えない。実測)。LABEL は位置引数で、rex は `-` で始まる値を flag と読んで拒むので、先頭の `-` を外す。
// 外して空なら label を付けない (rex の既定の名前になる)。
// rex-premise: preview.25 cli session new は LABEL --cwd --keep-open --window --json を取り、-- の後の argv をそのまま渡す
func (c *execClient) NewSession(ctx context.Context, label, cwd string, argv []string) (string, error) {
	label = strings.TrimLeft(label, "-")
	args := []string{"session", "new"}
	if label != "" {
		args = append(args, label)
	}
	args = append(args, "--cwd", cwd, "--keep-open")
	if label != "" {
		args = append(args, "--window", label)
	}
	args = append(args, "--json", "--")
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
