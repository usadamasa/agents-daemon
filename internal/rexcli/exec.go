// Package rexcli は rex CLI を叩く multiplexer.Client の実装を提供する｡
// rex の JSON は非公開の wire 型に閉じ､multiplexer.Pane へ写して返す｡
package rexcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// defaultTimeout は rex コマンド 1 回あたりのタイムアウト｡
const defaultTimeout = 10 * time.Second

// shellFlavor は terminal の block の flavor｡これ以外の block は program_status を持たない｡
const shellFlavor = "com.superlogical.terminal.shell"

// exitTargetNotFound は rex が block や session を解決できなかったときの exit code (実測)｡
const exitTargetNotFound = 3

// execClient は rex CLI を subprocess として呼び出す multiplexer.Client 実装｡
type execClient struct {
	binPath string
	timeout time.Duration
	// resolve は block_id から session_id を引く (statusline が書いた sidecar)｡
	resolve multiplexer.SessionResolver
}

// NewExecClient は実 rex バイナリを叩く Client を返す｡
// バイナリパスは環境変数 REX_BIN_PATH で上書きできる (未設定なら PATH 上の "rex")｡
func NewExecClient(resolve multiplexer.SessionResolver) multiplexer.Client {
	return newExecClientWithTimeout(resolve, defaultTimeout)
}

func newExecClientWithTimeout(resolve multiplexer.SessionResolver, timeout time.Duration) *execClient {
	bin := os.Getenv("REX_BIN_PATH")
	if bin == "" {
		bin = "rex"
	}
	return &execClient{binPath: bin, timeout: timeout, resolve: resolve}
}

// childEnv は rex に渡す環境｡REX_SESSION があると rex は block の解決をその session の中に
// 閉じるので､daemon が hook 経由で継承した値を外し､完全 ID で全 session の block を引けるようにする｡
func childEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "REX_SESSION=") || strings.HasPrefix(kv, "REX_BLOCK=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// run は rex を実行し stdout を返す｡失敗は stderr を添えた error にする｡
// timeout が 0 以下なら制限時間を課さない (テスト用)｡
func (c *execClient) run(ctx context.Context, args ...string) ([]byte, error) {
	runCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	// server が落ちているときに CLI が起こさないよう､全呼び出しで autostart を切る｡
	full := append([]string{"--autostart=false"}, args...)
	// #nosec G204 -- binPath は "rex" か REX_BIN_PATH の値 (利用者自身の環境)､args はこの
	// パッケージ内で組み立てた固定のサブコマンドと block ID のみ｡
	cmd := exec.CommandContext(runCtx, c.binPath, full...)
	cmd.Env = childEnv()
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// 期限超過で kill した後､パイプを継承した孫プロセスで Wait が止まらないようにする｡
	cmd.WaitDelay = 100 * time.Millisecond
	runErr := cmd.Run()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("rex %v: %s でタイムアウトした: %w", args, c.timeout, runCtx.Err())
	}
	if runErr != nil {
		return nil, fmt.Errorf("rex %v: %w (stderr=%q)", args, runErr, strings.TrimSpace(errBuf.String()))
	}
	return outBuf.Bytes(), nil
}

// runJSON は rex を実行し stdout の JSON を v へ読む｡
func (c *execClient) runJSON(ctx context.Context, v any, args ...string) error {
	out, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("rex %v: 応答の parse に失敗: %w (stdout=%q)", args, err, out)
	}
	return nil
}

// isTargetNotFound は rex が対象を解決できずに終わった (exit 3) かを判定する｡
func isTargetNotFound(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == exitTargetNotFound
}

type lsResult struct {
	Sessions []struct {
		SessionID string `json:"session_id"`
	} `json:"sessions"`
}

type blockLsResult struct {
	Blocks []struct {
		BlockID string `json:"block_id"`
		Flavor  string `json:"flavor"`
	} `json:"blocks"`
}

// programStatusResult は `block call program_status` の応答 (OSC 7501 の record)｡
type programStatusResult struct {
	Records []programStatusRecord `json:"records"`
}

// programStatusRecord の ID は root record では無いか空で､subagent では agent ID になる｡
// Owner は rex が足す､record を書いた process (OSC 7501 の仕様には無い)｡
type programStatusRecord struct {
	ID    string `json:"id"`
	State string `json:"state"`
	App   string `json:"app"`
	Owner struct {
		PID int `json:"pid"`
	} `json:"owner"`
}

// processResult は `block call process` の応答のうち使う部分｡
type processResult struct {
	Foreground *struct {
		PID   int    `json:"pid"`
		Argv0 string `json:"argv0"`
		CWD   string `json:"cwd"`
	} `json:"foreground"`
}

// claudeApp は Claude Code が OSC 7501 の app に書く値｡2.1.302 のバイナリでは固定で､
// rex preview.13 までが process 名から補っていた `claude` は preview.15 では来ない (#69)｡
// cc-premise: 2.1.302 osc7501 app=claude-code 固定
// rex-premise: preview.15 program_status rex は record を補わず､Claude Code の自己申告だけを返す
const claudeApp = "claude-code"

func (c *execClient) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	sessions, err := c.resolve()
	if err != nil {
		return nil, fmt.Errorf("block と session の対応の読み込みに失敗: %w", err)
	}

	var ls lsResult
	if err := c.runJSON(ctx, &ls, "ls", "--json"); err != nil {
		return nil, err
	}

	var panes []multiplexer.Pane
	for _, s := range ls.Sessions {
		// block ls だけは -s が必須 (無いと exit 2)｡
		var blocks blockLsResult
		if err := c.runJSON(ctx, &blocks, "block", "ls", "-s", s.SessionID, "--json"); err != nil {
			return nil, err
		}
		for _, b := range blocks.Blocks {
			if b.Flavor != shellFlavor {
				continue
			}
			pane, err := c.blockPane(ctx, b.BlockID, sessions)
			if err != nil {
				// block ls の後に閉じた block は読み飛ばす｡ここで失敗を返すと daemon が
				// PaneList の連続失敗に数え､block の開け閉めだけで終了しかねない｡
				if isTargetNotFound(err) {
					continue
				}
				return nil, err
			}
			panes = append(panes, pane)
		}
	}
	return panes, nil
}

func (c *execClient) PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error) {
	sessions, err := c.resolve()
	if err != nil {
		return multiplexer.Pane{}, fmt.Errorf("block と session の対応の読み込みに失敗: %w", err)
	}
	return c.blockPane(ctx, paneID, sessions)
}

// blockPane は 1 block に program_status と process を打って Pane を作る｡
func (c *execClient) blockPane(ctx context.Context, blockID string, sessions map[string]string) (multiplexer.Pane, error) {
	var ps programStatusResult
	if err := c.runJSON(ctx, &ps, "block", "call", "-b", blockID, "program_status"); err != nil {
		return multiplexer.Pane{}, err
	}
	var proc processResult
	if err := c.runJSON(ctx, &proc, "block", "call", "-b", blockID, "process"); err != nil {
		return multiplexer.Pane{}, err
	}
	return toPane(blockID, ps, proc, sessions[blockID]), nil
}

// toPane は rex の応答を中立の Pane へ写す｡TerminalTitle は consumer が読まないので埋めない
// (title method の分だけ fork が増える)｡
func toPane(blockID string, ps programStatusResult, proc processResult, sessionID string) multiplexer.Pane {
	pane := multiplexer.Pane{
		PaneID:      blockID,
		TerminalID:  blockID,
		AgentStatus: multiplexer.AgentStatusUnknown,
		SessionID:   sessionID,
	}
	if proc.Foreground != nil {
		pane.CWD = proc.Foreground.CWD
	}
	root := rootRecord(ps.Records)
	switch {
	case root != nil && root.App == claudeApp && isForeground(root, proc):
		pane.Agent = "claude"
		pane.AgentStatus = agentStatus(root.State)
	case proc.Foreground != nil && filepath.Base(proc.Foreground.Argv0) == "claude":
		// record が無い (起動直後の自己申告がまだ) か､record はあるが使えない｡
		// --shell=none で起動した block では argv0 が絶対パスになる (実測)｡
		pane.Agent = "claude"
		if root != nil && root.App == claudeApp {
			pane.UnknownReason = unverifiedReason(root, proc.Foreground.PID)
		}
	}
	return pane
}

// isForeground は record を書いた process が今も foreground にいるかを返す｡
// OSC 7501 の仕様では done と error の record は process の終了と shell prompt を生き延びるので､
// Claude Code が SIGKILL で終わる (/exit なら state=clear を出して消す) と素の shell に
// app=claude-code の done が残る (実測､#69)｡その block を Claude の pane と見て compact の催促や
// 再開の文面を shell へ打たないよう､foreground の pid が一致する record だけを使う｡
func isForeground(record *programStatusRecord, proc processResult) bool {
	return proc.Foreground != nil && record.Owner.PID == proc.Foreground.PID
}

// unverifiedReason は Claude Code の record を使えなかった理由｡owner が無いのは rex の版が変わって
// 欄が消えた (か名前が変わった) ときで､放置すると全 pane が unknown のまま compact の催促が止まる｡
func unverifiedReason(record *programStatusRecord, foregroundPID int) string {
	if record.Owner.PID == 0 {
		return "record に owner.pid が無い (rex の版が変わった可能性｡record を使えず agent_status が unknown のままになる)"
	}
	return fmt.Sprintf("record の owner (pid %d) が foreground の process (pid %d) と違う (前の Claude の record が残っている)", record.Owner.PID, foregroundPID)
}

// rootRecord は subagent ではない Claude Code 本体の record を返す｡無ければ nil｡
func rootRecord(records []programStatusRecord) *programStatusRecord {
	for i := range records {
		if records[i].ID == "" {
			return &records[i]
		}
	}
	return nil
}

func agentStatus(state string) multiplexer.AgentStatus {
	switch s := multiplexer.AgentStatus(state); s {
	case multiplexer.AgentStatusWorking, multiplexer.AgentStatusDone,
		multiplexer.AgentStatusIdle, multiplexer.AgentStatusBlocked:
		return s
	default:
		return multiplexer.AgentStatusUnknown
	}
}

// PaneRead は block の可視領域を返す (rex の capture は scrollback を読めない)｡
// opts.Lines > 0 なら末尾 Lines 行に絞る｡
func (c *execClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	out, err := c.run(ctx, "capture", "-b", paneID, "--trim", "--unwrap")
	if err != nil {
		return "", err
	}
	if opts.Lines > 0 {
		return tailLines(string(out), opts.Lines), nil
	}
	return string(out), nil
}

// tailLines は s の末尾 n 行を返す｡末尾の改行は行に数えず､あればそのまま残す｡
func tailLines(s string, n int) string {
	body, hasNL := strings.CutSuffix(s, "\n")
	lines := strings.Split(body, "\n")
	if len(lines) <= n {
		return s
	}
	tail := strings.Join(lines[len(lines)-n:], "\n")
	if hasNL {
		tail += "\n"
	}
	return tail
}

func (c *execClient) SendText(ctx context.Context, paneID, text string) error {
	_, err := c.run(ctx, "send", "-b", paneID, text)
	return err
}

func (c *execClient) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	_, err := c.run(ctx, append([]string{"send-key", "-b", paneID}, keys...)...)
	return err
}

type serverStatusResult struct {
	PID int `json:"pid"`
}

// Reachable は `api call server.status` で rex server の生死を確かめる｡
func (c *execClient) Reachable(ctx context.Context) error {
	var st serverStatusResult
	if err := c.runJSON(ctx, &st, "api", "call", "server.status"); err != nil {
		return err
	}
	if st.PID <= 0 {
		return errors.New("rex server の pid を取れない")
	}
	return nil
}
