package parkcli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/statecmd"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/tell"
)

// newTellCmd は `agents-daemon tell` を組み立てる (tell)。送る判定は tell.Runner が持ち、ここは rex の Client、上限の
// 画面の分類 (detect は runtime の家族で tell から import できない)、設定の待ち時間を注入する。
func newTellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tell",
		Short: "stdin の TellRequest の text を、to_session のセッションの pane の入力欄に打って Enter を送る",
		Long: "依存図のアプリ (gui/) が利用者の押した操作 (指示、タスクの着手や完了の依頼、メモの手渡し) をセッションへ届けるときに呼ぶ。\n" +
			"送る前に pane を読み、利用者の答えを待つ prompt が開いている、上限の画面、入力欄に打ちかけの文がある、claude が前景でない、\n" +
			"のどれかなら送らずに exit 1 で理由を出す。文面を打った後に prompt が開いたら、Enter を送らずに文面を入力欄に残して exit 1 で理由を出す。\n" +
			"送れたら TellResult を JSON で出す。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statecmd.Run(cmd, statecmd.Decoded("TellRequest", func(paths apppath.Paths, req protocol.TellRequest) (any, error) {
				runner, err := newTellRunner(paths)
				if err != nil {
					return nil, err
				}
				res, err := runner.Run(cmd.Context(), tell.Request{ToSession: req.ToSession, Text: req.Text})
				tellSent(paths.StateDir(), req.SessionID, req.ToSession, res.PaneID, err, time.Now())
				if err != nil {
					return nil, err
				}
				return res, nil
			}))
		},
	}
}

// newTellRunner は rex の Client と、daemon と同じ設定 (読む行数、待ち時間、追加の検知語) の tell.Runner を作る。
// 設定が読めなければ既定値で続ける (daemon の tick と同じ)。
func newTellRunner(paths apppath.Paths) (tell.Runner, error) {
	contexts := sessioncontext.New(paths.StateDir())
	cfg, _ := config.Load(paths.ConfigFile())
	classifier, err := detect.NewClassifier(cfg.CustomPatterns)
	if err != nil {
		return tell.Runner{}, err
	}
	return tell.Runner{
		Host: backend.ConnectRex(contexts),
		Blocks: func() (map[string]string, error) {
			return contexts.ListMultiplexerBlocks(sessioncontext.MultiplexerKindRex)
		},
		Screen: func(text string) string {
			if kind := classifier.Classify(text, cfg.DetectionTailLines); kind != detect.KindNone {
				return kind.String()
			}
			return ""
		},
		SubmitDelay: time.Duration(cfg.SubmitDelayMs) * time.Millisecond,
		ReadLines:   cfg.ReadLines,
	}, nil
}
