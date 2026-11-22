package parkcli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/tablabel"
)

// newTabLabelCmd は `agents-daemon tab-label` を組み立てる (tablabel)。mod (hooks/tab-label.ts) が呼ぶ。
func newTabLabelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tab-label",
		Short: "stdin の TabLabelRequest の block が居る rex のタブの名前を読み、label があれば付け直して TabLabelResult を出す",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("TabLabelRequest", func(paths apppath.Paths, req protocol.TabLabelRequest) (any, error) {
				return tabLabel(cmd.Context(), paths, req)
			}))
		},
	}
}

// tabLabel は req を rex の backend で処理する。
func tabLabel(ctx context.Context, paths apppath.Paths, req protocol.TabLabelRequest) (protocol.TabLabelResult, error) {
	client := backend.ConnectRex(sessioncontext.New(paths.StateDir()))
	defer backend.Close(client)
	host, ok := client.(tablabel.Host)
	if !ok {
		return protocol.TabLabelResult{}, errors.New("この backend はタブの名前を扱えない")
	}
	res, err := tablabel.Runner{Host: host}.Run(ctx, tablabel.Request{RexSession: req.RexSession, RexBlock: req.RexBlock, Label: req.Label})
	if err != nil {
		return protocol.TabLabelResult{}, err
	}
	return protocol.TabLabelResult{WindowID: res.WindowID, Label: res.Label, Status: string(res.Status)}, nil
}
