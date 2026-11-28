// Package desktopcli は Claude Code Desktop で始めたセッションを rex の画面で続けるコマンド (`agents-daemon desktop`) の
// 組み立てを持つ (#130)。Desktop 由来のセッションの列挙と閉じたかの確かめは claudesessions、cwd の補いは transcript、
// rex で開き直すのは spawn が持ち、ここは繋ぐだけ。
//
// rex には動いている process を block に取り込む口が無く、同じセッションを 2 つの process で開くと transcript に
// 両方の発言が混ざる。そのため Desktop で閉じてから、rex の新しい session で `claude --resume <id>` として開き直す
// (引き継ぎ)。Desktop へ戻すのは rex 側で `/desktop` を打つ (Claude Code 本体の口)。
package desktopcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/runtime/claudesessions"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/spawn"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// waitInterval は Desktop でセッションが閉じるのを待つ間に登録簿を引く間隔。利用者が閉じる操作を待つだけなので
// 1 秒で足りる。
const waitInterval = time.Second

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newDesktopCmd()}
}

func newDesktopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "desktop",
		Short: "Claude Code Desktop で始めたセッションを rex の画面で続ける (list / resume)",
	}
	cmd.AddCommand(newListCmd(), newResumeCmd())
	return cmd
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Desktop で開いているセッションを一覧する",
		Long: "Claude Code の登録簿 (sessions/<pid>.json) から、entrypoint が claude-desktop で process が生きている\n" +
			"セッションを出す。rex で続けるには、出た session ID を `agents-daemon desktop resume --wait <id>` に渡す。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := sessionStore()
			if err != nil {
				return err
			}
			sessions, err := store.Desktop()
			if err != nil {
				return err
			}
			if jsonflag.Enabled(cmd) {
				if sessions == nil {
					sessions = []claudesessions.Session{}
				}
				return jsonflag.Write(cmd.OutOrStdout(), sessions)
			}
			return writeList(cmd.OutOrStdout(), sessions)
		},
	}
}

func writeList(w io.Writer, sessions []claudesessions.Session) error {
	if len(sessions) == 0 {
		_, err := fmt.Fprintln(w, "Desktop で開いているセッションは無い")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SESSION\tSTATUS\tNAME\tCWD")
	for _, s := range sessions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.SessionID, s.Status, s.Name, s.Cwd)
	}
	return tw.Flush()
}

func newResumeCmd() *cobra.Command {
	var (
		wait time.Duration
		cwd  string
	)
	cmd := &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Desktop で閉じたセッションを、新しい rex の session で `claude --resume` として開く",
		Long: "Desktop でまだ開いているセッションは開かない (同じセッションを 2 つの process で開くと transcript が混ざる)。\n" +
			"--wait を付けると、Desktop でそのセッションが閉じるまで待ってから開く。\n" +
			"cwd は --cwd、Desktop で開いていれば登録簿、閉じていれば transcript から決める。\n" +
			"claude が前景になってから SpawnResult を JSON で出す。Desktop へ戻すときは rex 側で `/desktop` を打つ。\n" +
			"process の生存を signal 0 で確かめるので、sandbox の外 (`!`) で打つ。",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := resume(cmd.Context(), cmd.ErrOrStderr(), args[0], cwd, wait)
			if err != nil {
				return err
			}
			return jsonflag.Write(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 0, "Desktop でセッションが閉じるのを待つ上限 (0 なら待たずに、開いていれば error)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "開く cwd (絶対パス)。空なら登録簿か transcript から決める")
	return cmd
}

// resumer は resume の部品。テストが差し替える。
type resumer struct {
	sessions    claudesessions.Store
	transcripts transcript.Store
	runner      spawn.Runner
	interval    time.Duration
}

func resume(ctx context.Context, stderr io.Writer, sessionID, cwd string, wait time.Duration) (spawn.Result, error) {
	configDir, err := apppath.ClaudeConfigDir()
	if err != nil {
		return spawn.Result{}, err
	}
	paths, err := apppath.FromEnv()
	if err != nil {
		return spawn.Result{}, err
	}
	// Desktop から呼ばれるときは rex の外なので REX_BLOCK を要求しない (ConnectRex)。
	host, ok := backend.ConnectRex(sessioncontext.New(paths.StateDir())).(spawn.Host)
	if !ok {
		return spawn.Result{}, errors.New("この backend は session を作れない")
	}
	r := resumer{
		sessions:    claudesessions.New(configDir),
		transcripts: transcript.New(configDir),
		runner:      spawn.Runner{Host: host},
		interval:    waitInterval,
	}
	return r.run(ctx, stderr, sessionID, cwd, wait)
}

// run は Desktop で閉じたのを確かめて (--wait なら待って) から rex で開き直す。
func (r resumer) run(ctx context.Context, stderr io.Writer, sessionID, cwd string, wait time.Duration) (spawn.Result, error) {
	open, isOpen, err := r.sessions.Find(sessionID)
	if err != nil {
		return spawn.Result{}, err
	}
	if isOpen {
		if err := r.waitDesktopClosed(ctx, stderr, open, wait); err != nil {
			return spawn.Result{}, err
		}
		if cwd == "" {
			cwd = open.Cwd
		}
	}
	if cwd == "" {
		if cwd, err = r.transcripts.Cwd(sessionID); err != nil {
			return spawn.Result{}, err
		}
		if cwd == "" {
			return spawn.Result{}, fmt.Errorf("セッション %s の transcript から cwd が分からない。--cwd で指定する", sessionID)
		}
	}
	return r.runner.Resume(ctx, spawn.ResumeRequest{SessionID: sessionID, Cwd: cwd})
}

// sessionStore は Claude Code の設定ディレクトリの登録簿。
func sessionStore() (claudesessions.Store, error) {
	configDir, err := apppath.ClaudeConfigDir()
	if err != nil {
		return claudesessions.Store{}, err
	}
	return claudesessions.New(configDir), nil
}

// waitDesktopClosed は開いているセッション open が Desktop のものなら、wait の間 Desktop で閉じるのを待つ。
// 端末 (cli) で開いているもの、wait が 0 のもの、待っても閉じなかったものは error。
func (r resumer) waitDesktopClosed(ctx context.Context, stderr io.Writer, open claudesessions.Session, wait time.Duration) error {
	if open.Entrypoint != claudesessions.EntrypointDesktop {
		return fmt.Errorf("セッション %s は Desktop ではなく %s で開いている (pid %d)", open.SessionID, open.Entrypoint, open.PID)
	}
	if wait <= 0 {
		return fmt.Errorf("セッション %s は Desktop で開いている (pid %d)。Desktop で閉じてから打つか、--wait で閉じるのを待つ", open.SessionID, open.PID)
	}
	_, _ = fmt.Fprintf(stderr, "Desktop でセッション %s を閉じてください (%s まで待つ)\n", open.SessionID, wait)
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	err := r.sessions.WaitClosed(wctx, open.SessionID, r.interval)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return fmt.Errorf("%s の間にセッション %s が Desktop で閉じなかった", wait, open.SessionID)
	}
	return err
}
