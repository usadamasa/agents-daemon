package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/dream/adopt"
	"github.com/usadamasa/agents-daemon/internal/dream/dreamstate"
	"github.com/usadamasa/agents-daemon/internal/dream/forget"
	"github.com/usadamasa/agents-daemon/internal/dream/harvest"
	"github.com/usadamasa/agents-daemon/internal/dream/mine"
	"github.com/usadamasa/agents-daemon/internal/dream/stage"
)

// newDreamCmd は `agents-daemon dream` を組み立てる。
//
// dream は Claude Code の記憶階層 (CLAUDE.md / auto memory / skill) の consolidation で、
// 手順は `agents-daemon:dream` skill が持つ。ここにあるのはその実行系 5 phase。
// state は dreamstate.Dir(apppath の StateDir) に置き、各 phase は解決済みの絶対パスを受け取る。
func newDreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dream",
		Short: "記憶階層 (CLAUDE.md / auto memory / skill) の consolidation を支える 5 phase",
		Long: "Claude Code の記憶階層と skill 群を Harvest → Mine → Stage → Adopt の順に再編するための\n" +
			"サブコマンド群。forget は Consolidate で役目を終えた auto memory を消す。\n" +
			"いつ・何のために回すかは agents-daemon:dream skill が持つ。\n" +
			"state は ${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/ に置く。",
	}
	cmd.AddCommand(newDreamHarvestCmd())
	cmd.AddCommand(newDreamMineCmd())
	cmd.AddCommand(newDreamStageCmd())
	cmd.AddCommand(newDreamAdoptCmd())
	cmd.AddCommand(newDreamForgetCmd())
	return cmd
}

// dreamEnv は dream の各 phase が共通に要る環境由来の値。
type dreamEnv struct {
	home     string
	cwd      string
	stateDir string
}

// resolveDreamEnv は home・cwd・state ディレクトリを環境から 1 回で解決する。
// cwd は取れなくてもよい (project を flag で指定する phase がある) ので空で続ける。
func resolveDreamEnv() (dreamEnv, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return dreamEnv{}, err
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return dreamEnv{}, err
	}
	cwd, _ := os.Getwd()
	return dreamEnv{home: home, cwd: cwd, stateDir: dreamstate.Dir(p.StateDir())}, nil
}

// detachFlag は harvest を Stop hook から起こすときの flag。親はこれを外した引数で自分を detach 起動する。
const detachFlag = "--detach"

func newDreamHarvestCmd() *cobra.Command {
	var opts harvest.Opts
	var detach bool
	cmd := &cobra.Command{
		Use:   "harvest",
		Short: "Phase 1: session JSONL → harvest jsonl (PII 除去済み)",
		Long: "Claude Code の session JSONL を走査し、PII を除去して state の dream/harvest/YYYY-MM-DD.jsonl に\n" +
			"集約する。cwd 非依存で動作する (Stop hook から任意 project 下で呼ばれる)。\n" +
			"書き込みの前に dream/ 配下の保持期間を過ぎたファイルを消す。\n" +
			detachFlag + " は Stop hook 用で、同じ引数の自分を detach 起動して即座に戻る。子の stderr は\n" +
			"logs/dream-harvest.log に残り、成功すれば何も書かない。",
		Args: cobra.NoArgs,
		// 失敗の行には時刻を付ける。stderr が hook 経由でログに流れたとき、いつの失敗か分かるようにするため。
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runDreamHarvest(opts, detach)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s dream harvest: %v\n", time.Now().Format(time.RFC3339), err)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.ProjectsDir, "projects-dir", "", "Claude Code プロジェクトディレクトリ (デフォルト: ~/.claude/projects)")
	f.IntVar(&opts.Days, "days", 14, "走査する直近日数 (mtime ベース)")
	f.StringVar(&opts.ProjectGlob, "project-glob", "", "特定プロジェクトに絞る (canonical name の substring 一致)")
	f.BoolVar(&opts.Incremental, "incremental", false, "前回 harvest 以降に更新されたセッションのみ処理")
	f.BoolVar(&detach, strings.TrimPrefix(detachFlag, "--"), false, "同じ引数で自分を detach 起動し、待たずに戻る (Stop hook 用)")
	f.BoolVarP(&opts.Verbose, "verbose", "v", false, "処理サマリを stderr に出力")
	return cmd
}

// runDreamHarvest は detach なら自分を再起動して戻り、そうでなければこのプロセスで harvest を回す。
func runDreamHarvest(opts harvest.Opts, detach bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	if detach {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("実行ファイルパスの取得に失敗: %w", err)
		}
		return harvest.SpawnDetached(exe, childArgs(os.Args[1:]), harvest.DetachLogPath(filepath.Dir(p.LogFile())))
	}
	opts.Home = home
	opts.StateDir = dreamstate.Dir(p.StateDir())
	opts.Stderr = os.Stderr
	return harvest.Run(opts)
}

// childArgs は detach 起動する子に渡す引数。自分の引数から detach の flag だけを外す。
func childArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == detachFlag {
			continue
		}
		out = append(out, a)
	}
	return out
}

func newDreamMineCmd() *cobra.Command {
	var opts mine.Opts
	cmd := &cobra.Command{
		Use:   "mine",
		Short: "Phase 2: harvest + memory + skill → candidates.jsonl",
		Long: "harvest 済みセッションと対象 project の feedback memory・skill 本体を照合して、\n" +
			"skill 本体反映の migration 候補と重複 memory 削除候補を dream/candidates/<slug>-<date>.jsonl に出す。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := resolveDreamEnv()
			if err != nil {
				return err
			}
			opts.Home = env.home
			opts.Cwd = env.cwd
			opts.StateDir = env.stateDir
			opts.Stderr = cmd.ErrOrStderr()
			return mine.Run(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Project, "project", "", "対象 project (canonical name または絶対パス、省略時は cwd)")
	f.StringVar(&opts.HarvestDir, "harvest-dir", "", "harvest ログのディレクトリ (省略時は state の dream/harvest)")
	f.StringVar(&opts.MemoryDir, "memory-dir", "", "feedback memory のディレクトリ (省略時は ~/.claude/projects/<project>/memory)")
	f.StringVar(&opts.SkillsDir, "skills-dir", "", "skill 本体のディレクトリ (省略時は ~/.claude/skills)")
	f.StringVar(&opts.Date, "date", "", "candidates 出力ファイルの日付 (YYYY-MM-DD, 省略時は今日)")
	f.BoolVarP(&opts.Verbose, "verbose", "v", false, "処理サマリを stderr に出力")
	return cmd
}

func newDreamStageCmd() *cobra.Command {
	var opts stage.Opts
	cmd := &cobra.Command{
		Use:   "stage",
		Short: "Phase 6: candidates → Markdown レポート",
		Long: "mine が出力した candidates.jsonl を読み、user 承認 gate 用の Markdown レポートを\n" +
			"cwd の tmp/dream/YYYY-MM-DD-<slug>.md に生成する。LLM 呼び出しなし。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := resolveDreamEnv()
			if err != nil {
				return err
			}
			opts.Cwd = env.cwd
			opts.StateDir = env.stateDir
			opts.Stderr = cmd.ErrOrStderr()
			return stage.Run(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Project, "project", "", "対象 project (canonical name)、省略時はエラー")
	f.StringVar(&opts.Date, "date", "", "candidates 対象日 (YYYY-MM-DD, 省略時は今日)")
	f.StringVar(&opts.CandidatesFile, "candidates", "", "candidates.jsonl のパス (省略時は state の dream/candidates/<slug>-<date>.jsonl)")
	f.StringVar(&opts.OutFile, "out", "", "レポートの出力先 (省略時は ./tmp/dream/<date>-<slug>.md)")
	f.BoolVarP(&opts.Verbose, "verbose", "v", false, "処理サマリを stderr に出力")
	return cmd
}

func newDreamAdoptCmd() *cobra.Command {
	var opts adopt.Opts
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Phase 7: candidate → git branch + 手順書",
		Long: "candidate ID を受け取り、対象 skill/memory の実体 repo で作業ブランチ dream/YYYY-MM-DD-<slug> を\n" +
			"作成し、LLM/user が Edit + commit するための手順を stdout に出力する。\n" +
			"push は user 明示指示まで保留 (自動 push なし)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := resolveDreamEnv()
			if err != nil {
				return err
			}
			opts.Home = env.home
			opts.StateDir = env.stateDir
			opts.Stdout = cmd.OutOrStdout()
			opts.Stderr = cmd.ErrOrStderr()
			return adopt.Run(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.CandidateID, "candidate", "", "対象 candidate ID (必須)")
	f.StringVar(&opts.CandidatesFile, "candidates", "", "candidates.jsonl のパス (省略時は auto-resolve)")
	f.StringVar(&opts.Project, "project", "", "対象 project canonical (candidates ファイル自動解決に必要)")
	f.StringVar(&opts.Date, "date", "", "candidates 対象日 (YYYY-MM-DD, 省略時は今日)")
	f.BoolVar(&opts.DryRun, "dry-run", false, "branch 作成を skip し、コマンドだけ表示")
	return cmd
}

func newDreamForgetCmd() *cobra.Command {
	var opts forget.Opts
	cmd := &cobra.Command{
		Use:   "forget <memory-name>...",
		Short: "Phase 4: auto memory を削除し MEMORY.md の索引からも除く",
		Long: "指定した auto memory を memory ディレクトリから削除し、MEMORY.md の該当索引行も除去する。\n" +
			"削除対象は memory ディレクトリ直下の .md に限定され、MEMORY.md 自体と、ディレクトリ外を指す名前は拒否する。\n" +
			"1 件でも解決できない名前があれば何も削除しない。\n" +
			"~/.claude/projects/** は sandbox の保護対象なので、このコマンドは sandbox の外で動かす\n" +
			"(settings の sandbox.excludedCommands。agents-daemon:setup skill)。",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := resolveDreamEnv()
			if err != nil {
				return err
			}
			opts.Names = args
			opts.Home = env.home
			opts.Cwd = env.cwd
			opts.Stdout = cmd.OutOrStdout()
			opts.Stderr = cmd.ErrOrStderr()
			return forget.Run(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Project, "project", "", "対象 project (canonical name または絶対パス、省略時は cwd)")
	f.StringVar(&opts.MemoryDir, "memory-dir", "", "memory ディレクトリ (省略時は ~/.claude/projects/<project>/memory)")
	f.BoolVar(&opts.DryRun, "dry-run", false, "削除せず対象だけ表示する")
	return cmd
}
