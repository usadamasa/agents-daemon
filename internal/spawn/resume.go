package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// ResumeRequest は rex で開き直すセッションの指定 (#130)。
type ResumeRequest struct {
	// SessionID は開き直すセッション。Claude Code Desktop で始めて閉じたもの。
	SessionID string
	// Cwd は開き直すセッションの cwd (絶対パス)。transcript の置き場と合わないと `claude --resume` が見つけられない。
	Cwd string
}

// Resume は req のセッションを cwd にした新しい多重化ツールの session で `claude --resume <id>` として開き、その pane で
// claude が前景になってから結果を返す。error の扱いは Run と同じ。
//
// Desktop で開いたままのセッションを開かないのは呼び手 (desktop resume) の役目。開いたままでも、CLI 側で開いている
// セッションを Desktop へ戻す口 (`claude --desktop --resume`) は CLI 本体が止める。戻すのは rex 側で `/desktop` を打つ。
//
// cc-premise: 2.1.325 desktop Desktop で閉じたセッションは CLI の `claude --resume <id>` で続きが開き、CLI で開いている間は `/desktop` で Desktop へ戻せる (#130 で実測)
func (r Runner) Resume(ctx context.Context, req ResumeRequest) (Result, error) {
	if err := r.checkResume(req); err != nil {
		return Result{}, err
	}
	argv := []string{"claude", "--resume", req.SessionID}
	placed, err := r.Host.NewSession(ctx, filepath.Base(req.Cwd), req.Cwd, argv)
	if err != nil {
		return Result{}, fmt.Errorf("多重化ツールの session を作れない: %w", err)
	}
	res := Result{SessionID: req.SessionID, PaneID: placed.PaneID, RexSessionID: placed.SessionID, WindowID: placed.WindowID, Foreground: true}
	if err := r.waitForClaude(ctx, placed.PaneID); err != nil {
		if ctx.Err() != nil {
			return Result{}, err
		}
		res.Foreground, res.Detail = false, err.Error()
	}
	return res, nil
}

// checkResume は入力を確かめる。落ちるときは session を作らない。
func (r Runner) checkResume(req ResumeRequest) error {
	if req.SessionID == "" {
		return errors.New("session ID が空なので開かない")
	}
	if !statefile.ValidSessionID(req.SessionID) {
		return fmt.Errorf("session ID %q は使えない文字を含む", req.SessionID)
	}
	if !filepath.IsAbs(req.Cwd) {
		return fmt.Errorf("cwd は絶対パスで指定する: %q", req.Cwd)
	}
	stat := r.Stat
	if stat == nil {
		stat = os.Stat
	}
	if _, err := stat(req.Cwd); err != nil {
		return fmt.Errorf("cwd が見つからない: %s: %w", req.Cwd, err)
	}
	return nil
}
