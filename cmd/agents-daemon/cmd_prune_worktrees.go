package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/worktree"
)

// newPruneWorktreesCmd は `agents-daemon prune-worktrees` を組み立てる。
//
// 誰も使っていない Claude Code の worktree (`.claude/worktrees/` 配下) を回収する。判定と git 操作は
// internal/worktree が持つ。--hook は SessionStart hook (hooks/prune-worktrees.sh) 用で、stdin の hook 入力の
// cwd を起点にし、何があっても 0 で戻り、回収したときだけ systemMessage を stdout に出す。
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
				return runPruneWorktreesHook(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
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

// runPruneWorktreesHook は SessionStart hook として回収を回す。セッション開始を止めないことを最優先にし、
// git リポジトリ外でも、git が何を返しても nil を返す。失敗は stderr に残す。
func runPruneWorktreesHook(stdin io.Reader, stdout, stderr io.Writer, opts worktree.Opts) error {
	opts.Cwd = hookCwd(stdin)
	var report bytes.Buffer
	opts.Stdout = &report
	if err := worktree.Run(opts); err != nil && !errors.Is(err, worktree.ErrNotRepository) {
		_, _ = fmt.Fprintf(stderr, "%s prune-worktrees: %v\n", time.Now().Format(time.RFC3339), err)
	}
	if report.Len() == 0 {
		return nil
	}
	out, err := json.Marshal(map[string]string{"systemMessage": report.String()})
	if err != nil {
		return nil
	}
	_, _ = fmt.Fprintln(stdout, string(out))
	return nil
}

// hookCwd は hook 入力 (JSON) の cwd を返す。読めなければプロセスの cwd に落とす。
func hookCwd(stdin io.Reader) string {
	var input struct {
		Cwd string `json:"cwd"`
	}
	if err := json.NewDecoder(stdin).Decode(&input); err == nil && input.Cwd != "" {
		if info, err := os.Stat(input.Cwd); err == nil && info.IsDir() {
			return input.Cwd
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}
