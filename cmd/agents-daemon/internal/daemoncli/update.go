package daemoncli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/runtime/selfupdate"
)

// newUpdateCmd は `agents-daemon update` を組み立てる。
func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "plugin を更新し、更新した版で daemon を建て直す",
		Long: "claude plugin marketplace update、claude plugin update を走らせ、更新した版の\n" +
			"hooks/build-daemon.sh で daemon を建て直す。動いている daemon は次の tick で入れ替わる。\n" +
			"Claude Code の sandbox は ~/.claude/plugins と ~/.cache/agents-daemon に書けないので、\n" +
			"セッションの中からは `! ~/.cache/agents-daemon/bin/agents-daemon update` で打つ。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := claudeConfigDir()
			if err != nil {
				return err
			}
			run := func(name string, args ...string) error {
				cmd.PrintErrf("agents-daemon update: %s\n", strings.Join(append([]string{name}, args...), " "))
				c := exec.Command(name, args...) // #nosec G204 -- 固定の claude のサブコマンドと、installed_plugins.json の installPath の script
				c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
				return c.Run()
			}
			return selfupdate.Update(selfupdate.Options{ConfigDir: config, Run: run})
		},
	}
}

// claudeConfigDir は Claude Code の設定ディレクトリ。$CLAUDE_CONFIG_DIR があればそれ、無ければ ~/.claude。
func claudeConfigDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ホームディレクトリの取得に失敗: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}
