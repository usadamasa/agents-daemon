package daemoncli

import (
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/runtime/verify"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// newInspectCmd は `agents-daemon inspect` を組み立てる。--json は verify.InspectReport を出す。
func newInspectCmd() *cobra.Command {
	var paneID string
	var file string
	var sessionID string

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "transcript の末尾の分類と待機時刻の算出根拠、画面の通知欄の判定を表示する (送信はしない)",
		Long: "--pane <id> は rex 越しに生きた block を読み (REX_SESSION が要る)、--file <path> は保存済みの\n" +
			"画面テキストファイルを読む。どちらか一方を必ず指定する。\n" +
			"daemon (monitor パッケージ) と同じ経路で、pane の session_id (statusline の記録、無ければ前景の pid で\n" +
			"Claude Code の登録簿) を引き、その transcript の末尾の entry を分類し、上限なら起床時刻とその根拠を表示する。\n" +
			"画面は待機明けに送るキーを選ぶ通知欄の判定だけに使い、その走査した行も表示する。pane への送信は一切行わない。",
		RunE: func(cmd *cobra.Command, args []string) error {
			if (paneID == "") == (file == "") {
				return errors.New("--pane か --file のどちらか一方を指定してください")
			}
			p, err := apppath.FromEnv()
			if err != nil {
				return err
			}
			claudeDir, err := apppath.ClaudeConfigDir()
			if err != nil {
				return err
			}
			in := verify.InspectInput{Paths: p, ClaudeConfigDir: claudeDir, PaneID: paneID, File: file, SessionID: sessionID, Now: time.Now()}
			// --file は client に触らないので、rex の外でも保存済みの画面を読める。
			if paneID != "" {
				if _, in.Client, err = backend.Connect(os.Getenv, sessioncontext.New(p.StateDir())); err != nil {
					return err
				}
			}
			if !jsonflag.Enabled(cmd) {
				return verify.RunInspect(cmd.Context(), cmd.OutOrStdout(), in)
			}
			rep, err := verify.CollectInspect(cmd.Context(), in)
			if err != nil {
				return err
			}
			return jsonflag.Write(cmd.OutOrStdout(), rep)
		},
	}
	cmd.Flags().StringVar(&paneID, "pane", "", "rex 越しに読む block ID")
	cmd.Flags().StringVar(&file, "file", "", "保存済み画面テキストファイルのパス")
	cmd.Flags().StringVar(&sessionID, "session", "", "transcript を引く Claude セッション ID (--pane 指定時は pane から解決するため不要)")
	return cmd
}
