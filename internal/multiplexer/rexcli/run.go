package rexcli

import (
	"context"
	"fmt"
)

// runResult は `run --json` の応答。block_ids[0] が新しい window の pane。
// rex-premise: preview.21 cli run --json は window_id と block_ids を返す
type runResult struct {
	WindowID string   `json:"window_id"`
	BlockIDs []string `json:"block_ids"`
}

// RunWindow は `run` で新しい window を作り、その pane の ID を返す (spawn.Host)。`--focus=false` でフォーカスを奪わず、
// `--keep-open` で終了後も block を残す (起動の失敗を画面で読める)。`-s` は childEnv が REX_SESSION を外すので、
// session を明示して開いたセッションと同じ rex session に置く (空なら rex の既定)。argv は `--` の後にそのまま渡し、
// rex は shell を介さず 1 引数ずつ子へ渡す (空白、引用符、改行、`$` を変えない。実測)。
// rex-premise: preview.21 cli run は --focus --keep-open -s --cwd --label --json を取り、-- の後の argv をそのまま渡す
func (c *execClient) RunWindow(ctx context.Context, session, cwd, label string, argv []string) (string, error) {
	args := []string{"run", "--focus=false", "--keep-open"}
	if session != "" {
		args = append(args, "-s", session)
	}
	args = append(args, "--cwd", cwd)
	if label != "" {
		args = append(args, "--label", label)
	}
	args = append(args, "--json", "--")
	args = append(args, argv...)
	var res runResult
	if err := c.runJSON(ctx, &res, args...); err != nil {
		return "", err
	}
	if len(res.BlockIDs) == 0 {
		return "", fmt.Errorf("rex run: 応答の block_ids が空で、起こした pane が分からない (window_id=%q)", res.WindowID)
	}
	return res.BlockIDs[0], nil
}
