// Package worktreecli は `agents-daemon prune-worktrees` の組み立てを持つ。
package worktreecli

import (
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/worktree"
)

// Commands は root に登録するコマンド。
func Commands() []*cobra.Command {
	return []*cobra.Command{newPruneWorktreesCmd()}
}

// newPruneWorktreesCmd は `agents-daemon prune-worktrees` を組み立てる。
//
// 誰も使っていない Claude Code の worktree (`.claude/worktrees/` 配下) を回収する。判定・git 操作・hook 入力の
// 読み取りは internal/worktree が持つ。--hook は SessionStart hook (hooks/prune-worktrees.sh) 用。
func newPruneWorktreesCmd() *cobra.Command {
	var (
		dryRun, force, hook bool
		minAgeDays          int
	)
	cmd := &cobra.Command{
		Use:   "prune-worktrees",
		Short: "誰も使っていない Claude Code の worktree を回収する",
		Long: "cwd のリポジトリの `.claude/worktrees/` 配下で、lock が無いか foreground セッションの lock の PID が不在で、\n" +
			"最終更新から --min-age-days 日以上経ち、未コミットの変更が無い worktree を `git worktree remove` する。\n" +
			"ブランチは残す。subagent の lock (`claude agent`) は Claude Code 本体の sweep の担当なので触らない。\n" +
			"--hook は SessionStart hook 用で、stdin の hook 入力の cwd を起点にし、常に 0 で戻り、\n" +
			"回収したときだけ {\"systemMessage\": ...} を出す。\n" +
			"sandbox の中から手で叩くときは、main worktree の .git とその配下の worktree へ書けることが要る\n" +
			"(agents-daemon:agents-daemon skill の operations.md)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if minAgeDays < 0 {
				return errors.New("--min-age-days には 0 以上の整数を渡してください")
			}
			opts := worktree.Opts{
				MinAge: time.Duration(minAgeDays) * 24 * time.Hour,
				Force:  force,
				DryRun: dryRun,
			}
			if hook {
				return worktree.RunHook(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			opts.Cwd = cwd
			opts.Stdout = cmd.OutOrStdout()
			return worktree.Run(opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "削除せず対象だけを表示する")
	f.BoolVar(&force, "force", false, "未コミットの変更がある worktree も削除する")
	f.IntVar(&minAgeDays, "min-age-days", int(worktree.DefaultMinAge/(24*time.Hour)), "N 日以上経過したものだけ回収する")
	f.BoolVar(&hook, "hook", false, "SessionStart hook として動く (stdin の cwd を使い、常に 0 で戻る)")
	return cmd
}
