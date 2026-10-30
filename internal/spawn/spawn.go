// Package spawn は、パーキングのメモを渡す先として新しい Claude Code のセッションを多重化ツールの window に起こす (#162)。
//
// 起こすのは daemon のバイナリ (`agents-daemon spawn`)。mod や hook から多重化ツールを叩かない決めごとに合わせる。
// 新しいセッションは repo を cwd にし、session ID をこちらで決めて `claude --session-id <id> --model <model> -- <本文>`
// で起こす。本文は最初のターンとして入るので、peer メッセージの inbox が開くのを待たずに渡せる。
//
// window を起こす口 (Host) は multiplexer.Client に足さず、ここに閉じる。Client は daemon / monitor / doctor / verify が
// pane を見て送るための全部入りで、window の作成はこのコマンドだけが使う (simulate の simulateHost と同じ考え)。
package spawn

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// 既定の待ち時間。claude は login shell を経て起きるので数秒かかることがある。
const (
	defaultWaitTimeout  = 15 * time.Second
	defaultWaitInterval = 200 * time.Millisecond
)

// Host は起こす先の多重化ツール。rexcli の Client が満たす。
type Host interface {
	// RunWindow は新しい window を作り、その pane で argv を起こして pane の ID を返す。フォーカスは奪わず、コマンドが
	// 終わっても pane を残す (起動の失敗を画面で読めるようにする)。window ができた時点で返り、コマンドの起動は待たない。
	// session は window を置く多重化ツールの session (rex なら REX_SESSION の値。空なら既定)、label は pane の名前 (空なら付けない)。
	RunWindow(ctx context.Context, session, cwd, label string, argv []string) (paneID string, err error)
	// PaneGet は pane の今の前景 (multiplexer.Client と同じ)。起こした pane で claude が前景になるのを待つのに使う。
	PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error)
}

// Request は起こすセッションの指定。
type Request struct {
	// Repo は新しいセッションの cwd (絶対パス)。
	Repo string
	// Prompt は最初のターンとして入る本文。
	Prompt string
	// Model は新しいセッションのモデル。別セッションへ渡す作業は subagent 用のモデルに揃える (peer-sessions skill) ので、
	// CLI は CLAUDE_CODE_SUBAGENT_MODEL を入れる。空なら起こさない。
	Model string
	// MultiplexerSession は window を置く多重化ツールの session (rex なら REX_SESSION の値)。
	MultiplexerSession string
}

// Result は起こしたセッション。JSON で stdout に出す。
type Result struct {
	SessionID string `json:"session_id"`
	PaneID    string `json:"pane_id"`
	// Foreground は pane で claude が前景になった (本文が最初のターンとして入った) か。false なら待ち切れで、pane は残り
	// claude が起きているかもしれない。呼び出し側はメモを戻さず、Detail を人に見せる。
	Foreground bool   `json:"foreground"`
	Detail     string `json:"detail,omitempty"`
}

// Runner は起こす手段。テストは Host を差し替える。
type Runner struct {
	Host Host
	// NewSessionID は新しいセッションの ID。nil なら newSessionID (UUID v4)。
	NewSessionID func() (string, error)
	// Stat は repo の存在の確認。nil なら os.Stat。
	Stat func(string) (os.FileInfo, error)
	// WaitTimeout と WaitInterval は、起こした pane で claude が前景になるのを待つ上限と間隔。零なら既定。
	WaitTimeout  time.Duration
	WaitInterval time.Duration
}

// Run は req のセッションを起こし、その pane で claude が前景になってから結果を返す。error は window を作る前か
// 作れなかったときだけ。前景にならなかったときは window ができているので Foreground=false で返す (pane は残るので、
// 起動の失敗は画面で読める)。
func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	if err := r.check(req); err != nil {
		return Result{}, err
	}
	newID := r.NewSessionID
	if newID == nil {
		newID = newSessionID
	}
	id, err := newID()
	if err != nil {
		return Result{}, fmt.Errorf("session ID を作れない: %w", err)
	}
	// `--` は本文が `-` で始まっても flag と読まれないため。
	argv := []string{"claude", "--session-id", id, "--model", req.Model, "--", req.Prompt}
	paneID, err := r.Host.RunWindow(ctx, req.MultiplexerSession, req.Repo, filepath.Base(req.Repo), argv)
	if err != nil {
		return Result{}, fmt.Errorf("window を作れない: %w", err)
	}
	res := Result{SessionID: id, PaneID: paneID, Foreground: true}
	if err := r.waitForClaude(ctx, paneID); err != nil {
		if ctx.Err() != nil {
			return Result{}, err
		}
		res.Foreground, res.Detail = false, err.Error()
	}
	return res, nil
}

// check は入力を確かめる。落ちるときは window を作らない。
func (r Runner) check(req Request) error {
	if !filepath.IsAbs(req.Repo) {
		return fmt.Errorf("repo は絶対パスで指定する: %q", req.Repo)
	}
	stat := r.Stat
	if stat == nil {
		stat = os.Stat
	}
	if _, err := stat(req.Repo); err != nil {
		return fmt.Errorf("repo が見つからない: %s: %w", req.Repo, err)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return errors.New("本文が空なので起こさない")
	}
	if req.Model == "" {
		return errors.New("モデルが無いので起こさない (CLAUDE_CODE_SUBAGENT_MODEL を settings の env に定義する)")
	}
	return nil
}

// waitForClaude は pane で claude が前景になるのを待つ。block ができてから login shell が claude を起こすまでの間は
// PaneGet が not found や shell が前景の Pane を返すので、上限まで数えずに待つ。
func (r Runner) waitForClaude(ctx context.Context, paneID string) error {
	timeout, interval := r.WaitTimeout, r.WaitInterval
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	if interval <= 0 {
		interval = defaultWaitInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		if pane, err := r.Host.PaneGet(ctx, paneID); err == nil && multiplexer.IsClaudeAgent(pane) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s の間に pane %s で claude が前景にならなかった (pane は残してある。画面で起動の失敗を確かめる)", timeout, paneID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// newSessionID は UUID v4 を返す。Claude Code の --session-id が受ける形。
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
