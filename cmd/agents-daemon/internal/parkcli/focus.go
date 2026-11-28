package parkcli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/focus"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// newFocusCmd は `agents-daemon focus` を組み立てる (focus)。依存図のアプリ (gui/) が呼ぶ。
func newFocusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "focus",
		Short: "stdin の FocusRequest の to_session のセッションの pane の居る window を、Rex.app に表示させる",
		Long: "依存図のアプリ (gui/) が、利用者をセッションの pane へ連れて行くときに呼ぶ。\n" +
			"pane が見つからない、この機械の Rex.app が rex の server に繋がっていない、Rex.app が断った (Remote Control が無効)、\n" +
			"のどれかなら exit 1 で理由を出す。表示させたら FocusResult を JSON で出す。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("FocusRequest", func(paths apppath.Paths, req protocol.FocusRequest) (any, error) {
				contexts := sessioncontext.New(paths.StateDir())
				client := backend.ConnectRex(contexts)
				defer backend.Close(client)
				host, ok := client.(focus.Host)
				if !ok {
					return nil, errors.New("この backend は window の表示を扱えない")
				}
				runner := focus.Runner{Host: host, Blocks: func() (map[string]string, error) {
					return contexts.ListMultiplexerBlocks(sessioncontext.MultiplexerKindRex)
				}}
				res, err := runner.Run(cmd.Context(), focus.Request{ToSession: req.ToSession})
				focusShown(paths.StateDir(), req.SessionID, req.ToSession, res.PaneID, err, time.Now())
				if err != nil {
					return nil, err
				}
				return res, nil
			}))
		},
	}
}
