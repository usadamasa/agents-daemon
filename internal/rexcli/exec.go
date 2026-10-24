// Package rexcli は rex CLI を叩く multiplexer.Client の実装を提供する。
// rex の JSON は非公開の wire 型に閉じ、multiplexer.Pane へ写して返す。
// CLI の呼び出しは exec.go、record から Pane への写像は pane.go が持つ。
package rexcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// defaultTimeout は rex コマンド 1 回あたりのタイムアウト。
const defaultTimeout = 10 * time.Second

// shellFlavor は terminal の block の flavor。これ以外の block は program_status を持たない。
// rex-premise: preview.19 cli block ls の flavor の値
const shellFlavor = "com.superlogical.terminal.shell"

// exitTargetNotFound は rex が block や session を解決できなかったときの exit code (実測)。
// rex-premise: preview.19 cli 存在しない block への block call は exit 3
const exitTargetNotFound = 3

// SessionResolver は block の ID から Claude Code の session_id への対応を返す。
// 実体は statusline が `context/<sid>.json` の multiplexer に書いた記録 (sessioncontext.Store.ListMultiplexerBlocks)。
// PaneList / PaneGet の呼び出しごとに 1 回だけ呼ぶ。
type SessionResolver func() (map[string]string, error)

// execClient は rex CLI を subprocess として呼び出す multiplexer.Client 実装。
type execClient struct {
	// binPath は解決済みの rex の絶対パス。空なら次の呼び出しで locateBinary が埋める。
	binPath string
	timeout time.Duration
	// resolve は block_id から session_id を引く (statusline が書いた sidecar)。
	resolve SessionResolver
}

// NewExecClient は実 rex バイナリを叩く Client を返す。
// バイナリは最初の呼び出しで PATH から引く。環境変数 REX_BIN_PATH があればそれで固定する。
func NewExecClient(resolve SessionResolver) multiplexer.Client {
	return newExecClientWithTimeout(resolve, defaultTimeout)
}

func newExecClientWithTimeout(resolve SessionResolver, timeout time.Duration) *execClient {
	return &execClient{binPath: os.Getenv("REX_BIN_PATH"), timeout: timeout, resolve: resolve}
}

// bin は rex の絶対パスを返す。未解決なら PATH から引いて覚える。
func (c *execClient) bin() (string, error) {
	if c.binPath == "" {
		bin, err := locateBinary()
		if err != nil {
			return "", err
		}
		c.binPath = bin
	}
	return c.binPath, nil
}

// childEnv は rex に渡す環境。REX_SESSION があると rex は block の解決をその session の中に
// 閉じるので、daemon が hook 経由で継承した値を外し、完全 ID で全 session の block を引けるようにする。
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

// run は rex を実行し stdout を返す。失敗は stderr を添えた error にする。
// rex の自動更新で覚えていたバイナリが消えていたら、引き直して同じ呼び出しを 1 回だけやり直す
// (tick の失敗に数えさせない)。REX_BIN_PATH で固定していれば引き直しても同じパスなので、そのまま失敗を返す。
func (c *execClient) run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := c.bin()
	if err != nil {
		return nil, fmt.Errorf("rex %v: %w", args, err)
	}
	out, err := c.runWith(ctx, bin, args...)
	if !binaryVanished(err) {
		return out, err
	}
	c.binPath = ""
	again, locateErr := c.bin()
	if locateErr != nil || again == bin {
		return nil, err
	}
	return c.runWith(ctx, again, args...)
}

// runWith は bin を rex として実行する。timeout が 0 以下なら制限時間を課さない (テスト用)。
func (c *execClient) runWith(ctx context.Context, bin string, args ...string) ([]byte, error) {
	runCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	// server が落ちているときに CLI が起こさないよう、全呼び出しで autostart を切る。
	// rex-premise: preview.19 cli --autostart は global option で既定 true
	full := append([]string{"--autostart=false"}, args...)
	// #nosec G204 -- bin は PATH か REX_BIN_PATH から引いた rex (利用者自身の環境)、args はこの
	// パッケージ内で組み立てた固定のサブコマンドと block ID のみ。
	cmd := exec.CommandContext(runCtx, bin, full...)
	cmd.Env = childEnv()
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// 期限超過で kill した後、パイプを継承した孫プロセスで Wait が止まらないようにする。
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

// runJSON は rex を実行し stdout の JSON を v へ読む。
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

// isTargetNotFound は rex が対象を解決できずに終わった (exit 3) かを判定する。
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
		// block ls だけは -s が必須 (無いと exit 2)。
		// rex-premise: preview.19 cli block ls は -s 必須
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
				// block ls の後に閉じた block は読み飛ばす。ここで失敗を返すと daemon が
				// PaneList の連続失敗に数え、block の開け閉めだけで終了しかねない。
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

// blockPane は 1 block に program_status と process を打って Pane を作る。
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

// PaneRead は block の画面を返す。Claude Code は alternate screen で描くので、Claude の block では
// 可視の行数で頭打ちになる (alternate screen でない block では scrollback も返る)。
// opts.Lines > 0 なら末尾 Lines 行に絞る。
// rex-premise: preview.19 cli capture は alternate screen では可視の行だけ、通常の画面では scrollback も返す。--trim と --unwrap がある
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

type sizeResult struct {
	Rows    int `json:"rows"`
	Columns int `json:"columns"`
}

type modesResult struct {
	Screen string `json:"screen"`
}

// screenAlternate は modes の screen が alternate screen を指す値。これ以外は primary と見る (output_schema の指示)。
const screenAlternate = "alternate"

// PaneGeometry は size と modes の 2 回で block の高さと screen を返す。modes は idle の terminal を起こさない
// (changelog)。
// rex-premise: preview.19 cli block call size は rows と columns を返す
// rex-premise: preview.19 cli block call modes は screen (primary / alternate) を返す
func (c *execClient) PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error) {
	var size sizeResult
	if err := c.runJSON(ctx, &size, "block", "call", "-b", paneID, "size"); err != nil {
		return multiplexer.Geometry{}, err
	}
	var modes modesResult
	if err := c.runJSON(ctx, &modes, "block", "call", "-b", paneID, "modes"); err != nil {
		return multiplexer.Geometry{}, err
	}
	return multiplexer.Geometry{Rows: size.Rows, Columns: size.Columns, AlternateScreen: modes.Screen == screenAlternate}, nil
}

// tailLines は s の末尾 n 行を返す。末尾の改行は行に数えず、あればそのまま残す。
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

// Reachable は `api call server.status` で rex server の生死を確かめる。
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
