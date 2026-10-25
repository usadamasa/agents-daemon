// Package stage は dream 7-phase pipeline の Phase 6 Stage を提供する。
//
// mine が出力した candidates.jsonl を読み、user 承認 gate 用の Markdown レポートを
// cwd の tmp/dream/YYYY-MM-DD-<slug>.md として生成する。
// LLM 呼び出しなし。proposed_change の diff 本文は Adopt phase で埋める。
package stage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/dreamio"
)

// Opts は stage サブコマンドの実行パラメータ。
type Opts struct {
	Project        string // 必須 (canonical name)
	Date           string // 空なら今日
	CandidatesFile string // 空なら StateDir/candidates/<slug>-<date>.jsonl
	OutFile        string // 空なら Cwd/tmp/dream/<date>-<slug>.md
	StateDir       string // dream の state ディレクトリ (dream.Dir)。CandidatesFile 未指定時に必要
	Verbose        bool

	Cwd    string // os.Getwd 相当 (呼び出し側で解決)。OutFile 未指定時に必要
	Stderr io.Writer
}

// Run は stage phase 一回分を実行する。
func Run(opts Opts) error {
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Project == "" {
		return errors.New("--project は必須 (canonical name を指定)")
	}

	resolvedDate := dream.ResolveDate(opts.Date)
	resolvedCandidates := opts.CandidatesFile
	if resolvedCandidates == "" {
		if opts.StateDir == "" {
			return errors.New("StateDir が未解決 (呼び出し側が apppath で決める)")
		}
		resolvedCandidates = dreamio.CandidatesPath(opts.StateDir, opts.Project, resolvedDate)
	}
	outFile := opts.OutFile
	if outFile == "" {
		if opts.Cwd == "" {
			return errors.New("--out 未指定かつ cwd も不明")
		}
		outFile = reportPath(opts.Cwd, opts.Project, resolvedDate)
	}

	cands, err := dreamio.ReadCandidates(resolvedCandidates)
	if err != nil {
		return fmt.Errorf("candidates 読み込み失敗 (%s): %w", resolvedCandidates, err)
	}

	report := renderReport(opts.Project, resolvedDate, cands)
	if err := os.MkdirAll(filepath.Dir(outFile), 0o750); err != nil {
		return fmt.Errorf("出力ディレクトリ作成失敗: %w", err)
	}
	if err := os.WriteFile(outFile, []byte(report), 0o600); err != nil {
		return fmt.Errorf("レポート書き込み失敗: %w", err)
	}

	if opts.Verbose {
		_, _ = fmt.Fprintf(opts.Stderr, "project=%s candidates=%d report=%s\n",
			opts.Project, len(cands), outFile)
	}
	return nil
}
