package rexcli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/api"
)

// SharedSessionEnv は、タブの包みが起こすコマンドに渡す環境変数。値は 1。plugin の SessionStart hook
// (hooks/shared-rex-session.sh) がこれを見て、起きた claude に session を rename させない案内を出す。
const SharedSessionEnv = "AGENTS_DAEMON_SHARED_REX_SESSION"

// windowNewResult は session.new_window の応答。block_ids[0] が新しい window の pane。
// rex-premise: preview.30 api session.new_window は window_id と block_ids を返す
type windowNewResult struct {
	WindowID string   `json:"window_id"`
	BlockIDs []string `json:"block_ids"`
}

// closeOnExitTabScript は既存の session に足したタブでコマンドを走らせる sh の本文。$1 以降が起こす argv。
// 足し先の session はほかの作業と共有するので、印を書かず session も閉じない。コマンドが終われば pane が閉じ、
// 最後の pane を失った window は rex が閉じるので、ほかのタブは残る。set -m (サイドバーの題) と 0 以外で終わったときの
// 待ちは closeOnExitScript と同じ。
// peer-sessions skill の references/rex.md が同じ本文を single quote で囲んで載せるので、本文に single quote を入れない。
// rex-premise: preview.30 cli window new のコマンドが終わると pane と window が閉じ、session は残る
const closeOnExitTabScript = `set -m; export ` + SharedSessionEnv + `=1; "$@"; s=$?; c() { exit "$s"; }; ` +
	`if [ "$s" -ne 0 ]; then trap c INT; printf "\n%s が exit %s で終わった。Enter で閉じる。\n" "$1" "$s"; read -r _; fi; c`

// NewWindow は既存の rex session (ID か label) にタブ (window) を足し、その pane で argv を起こして pane の ID を返す
// (spawn.Host)。label は window の名前で、先頭の `-` は NewSession と同じく外す。利用者が見ている window を動かさないよう
// focus を false にする。argv は closeOnExitTabScript で包む。足し先が無ければ not found の error を返す。
func (c *execClient) NewWindow(ctx context.Context, session, label, cwd string, argv []string) (string, error) {
	if session == "" {
		return "", errors.New("rex のタブを足せない: 足し先の session が空")
	}
	sessionID, err := c.rex.SessionByName(ctx, session)
	if err != nil {
		return "", err
	}
	label = strings.TrimLeft(label, "-")
	command := append([]string{"/bin/sh", "-c", closeOnExitTabScript, "agents-daemon-spawn"}, argv...)
	req := map[string]any{
		"session_id": sessionID, "focus": false,
		"layout": api.ShellLayout("", map[string]any{"cwd": cwd, "command": command}),
	}
	if label != "" {
		req["window_label"] = label
	}
	var res windowNewResult
	if err := c.rex.Call(ctx, "session.new_window", req, &res); err != nil {
		return "", err
	}
	if len(res.BlockIDs) == 0 {
		return "", fmt.Errorf("rex の session.new_window の応答の block_ids が空で、起こした pane が分からない (window_id=%q)", res.WindowID)
	}
	block := res.BlockIDs[0]
	_ = c.rex.Release(ctx, sessionID, []string{block}) // 手放せなくても pane は動いている (api.Client.Release)
	return block, nil
}
