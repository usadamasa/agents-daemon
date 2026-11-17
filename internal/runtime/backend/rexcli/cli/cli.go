// Package cli は rex の CLI を子プロセスとして起こす層。バイナリの探索と自動更新で消えたバイナリの引き直し、
// 子の環境、--autostart=false の付与、対象が無いことの exit code の判定を持つ。
// rexcli の書き込み系と events の購読、premise の点検が使う (読み取りは control API へ移った)。
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// exitTargetNotFound は rex が block や session を解決できなかったときの exit code (実測)。
// rex-premise: preview.30 cli 存在しない block への block call は exit 3
const exitTargetNotFound = 3

// Runner は rex を起こす口。解決した rex のパスを覚え、並んだ呼び出しの間で共有する。
type Runner struct {
	// mu は binPath を守る。購読の goroutine と daemon の tick が並んで rex を呼ぶ。
	mu sync.Mutex
	// binPath は解決済みの rex の絶対パス。空なら次の呼び出しで locateBinary が埋める。
	binPath string
	// timeout は 1 回の起動の制限時間。0 以下なら課さない (テスト用)。
	timeout time.Duration
}

// New は binPath の rex を起こす Runner を返す。binPath が空なら最初の呼び出しで引く。
func New(binPath string, timeout time.Duration) *Runner {
	return &Runner{binPath: binPath, timeout: timeout}
}

// FromEnv は REX_BIN_PATH があればそれで固定し、無ければ最初の呼び出しで PATH から引く Runner を返す。
func FromEnv(timeout time.Duration) *Runner {
	return New(os.Getenv("REX_BIN_PATH"), timeout)
}

// Bin は rex の絶対パスを返す。未解決なら引いて覚える。
func (r *Runner) Bin() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.binPath == "" {
		bin, err := locateBinary()
		if err != nil {
			return "", err
		}
		r.binPath = bin
	}
	return r.binPath, nil
}

// BinPath は覚えている rex のパスを返す (未解決なら空)。
func (r *Runner) BinPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.binPath
}

// Forget は覚えていた rex が消えたとき、次の Bin で引き直させる。並んだ呼び出しが先に引き直していれば触らない。
// REX_BIN_PATH で固定していれば引き直しても同じパスになる。
func (r *Runner) Forget(stale string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.binPath == stale {
		r.binPath = ""
	}
}

// Env は rex に渡す環境。REX_SESSION があると rex は block の解決をその session の中に
// 閉じるので、daemon が hook 経由で継承した値を外し、完全 ID で全 session の block を引けるようにする。
func Env() []string {
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

// Run は rex を実行し stdout を返す。失敗は stderr を添えた error にする。
// rex の自動更新で覚えていたバイナリが消えていたら、引き直して同じ呼び出しを 1 回だけやり直す
// (tick の失敗に数えさせない)。REX_BIN_PATH で固定していれば引き直しても同じパスなので、そのまま失敗を返す。
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := r.Bin()
	if err != nil {
		return nil, fmt.Errorf("rex %v: %w", args, err)
	}
	out, err := r.runWith(ctx, bin, args...)
	if !Vanished(err) {
		return out, err
	}
	r.Forget(bin)
	again, locateErr := r.Bin()
	if locateErr != nil || again == bin {
		return nil, err
	}
	return r.runWith(ctx, again, args...)
}

// runWith は bin を rex として実行する。
func (r *Runner) runWith(ctx context.Context, bin string, args ...string) ([]byte, error) {
	runCtx := ctx
	if r.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	// server が落ちているときに CLI が起こさないよう、全呼び出しで autostart を切る。
	// rex-premise: preview.30 cli --autostart は global option で既定 true
	full := append([]string{"--autostart=false"}, args...)
	// #nosec G204 -- bin は PATH か REX_BIN_PATH から引いた rex (利用者自身の環境)、args は呼び手の
	// パッケージ内で組み立てた固定のサブコマンドと block ID のみ。
	cmd := exec.CommandContext(runCtx, bin, full...)
	cmd.Env = Env()
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// 期限超過で kill した後、パイプを継承した孫プロセスで Wait が止まらないようにする。
	cmd.WaitDelay = 100 * time.Millisecond
	runErr := cmd.Run()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("rex %v: %s でタイムアウトした: %w", args, r.timeout, runCtx.Err())
	}
	if runErr != nil {
		return nil, fmt.Errorf("rex %v: %w (stderr=%q)", args, runErr, strings.TrimSpace(errBuf.String()))
	}
	return outBuf.Bytes(), nil
}

// RunJSON は rex を実行し stdout の JSON を v へ読む。
func (r *Runner) RunJSON(ctx context.Context, v any, args ...string) error {
	out, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("rex %v: 応答の parse に失敗: %w (stdout=%q)", args, err, out)
	}
	return nil
}

// TargetNotFound は rex が対象を解決できずに終わった (exit 3) かを判定する。
func TargetNotFound(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == exitTargetNotFound
}
