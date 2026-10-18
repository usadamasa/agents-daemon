package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/board"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/verify"
)

// newStatusCmd は `agents-daemon status` を組み立てる。
//
// --board はタスクのボードの JSON (internal/board) を出す。plugin の mod (hooks/board.tsx) が数秒ごとに読む。
// 独立のサブコマンドにしないのは、cmd パッケージが analyze-modularity の行数の上限に近いため。
func newStatusCmd() *cobra.Command {
	var (
		asBoard bool
		viewer  board.Viewer
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "daemon の稼働状況と監視中の pane 一覧を表示する",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			if !asBoard {
				return verify.RunStatus(cmd.OutOrStdout(), p, time.Now())
			}
			if viewer.CWD == "" {
				if viewer.CWD, err = os.Getwd(); err != nil {
					return err
				}
			}
			return board.Write(cmd.OutOrStdout(), p, tasklog.LoadPlacer(p, os.Getenv("HOME")), viewer)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&asBoard, "board", false, "タスクのボードを JSON で出す (plugin の mod hooks/board.tsx が読む)")
	f.StringVar(&viewer.CWD, "cwd", "", "--board: ボードを開いたセッションの cwd。この domain だけを描く (既定は今の cwd)")
	f.StringVar(&viewer.SessionID, "session", "", "--board: ボードを開いたセッションの ID。記録が業務の側にあれば業務を描く")
	return cmd
}
