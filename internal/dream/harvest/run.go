// Package harvest は dream 7-phase pipeline の Phase 1 Harvest を提供する。
//
// Claude Code の session JSONL を走査し、PII を除去して state ディレクトリ配下に
// 1 行 1 セッションの harvest jsonl として集約する。
//
// cwd 非依存で動作する (Stop hook から任意 project 下で呼ばれるため)。
package harvest

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/dream/dreamstate"
	"github.com/usadamasa/agents-daemon/internal/dream/jsonlscan"
)

// Opts は harvest サブコマンドの実行パラメータ。値は呼び出し側 (cobra) で解決済み。
type Opts struct {
	ProjectsDir string // 空なら ~/.claude/projects
	StateDir    string // dream の state ディレクトリ (dreamstate.Dir)。必須
	Days        int    // 走査する直近日数 (mtime ベース)
	ProjectGlob string // canonical name substring 一致
	Incremental bool   // 前回 harvest 以降のみ
	Verbose     bool   // 処理サマリを Stderr へ
	Home        string // os.UserHomeDir 相当 (呼び出し側で解決)
	Stderr      io.Writer
}

// Run は harvest phase 一回分を実行する。呼び出し側は cobra Command から Opts を組み立てて呼ぶ。
//
// 書き込みの前に state ディレクトリの保持期間を過ぎたファイルを消す。daemon の housekeeping は
// rex の外では走らないので、Stop のたびに起きるこのコマンドが書き手として掃除する。
func Run(opts Opts) error {
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.StateDir == "" {
		return errors.New("StateDir が未解決 (呼び出し側が apppath で決める)")
	}

	resolvedProjects := opts.ProjectsDir
	if resolvedProjects == "" {
		resolvedProjects = filepath.Join(opts.Home, ".claude", "projects")
	}

	state, err := loadState(opts.StateDir)
	if err != nil {
		return fmt.Errorf("state 読み込み失敗: %w", err)
	}

	runStart := time.Now()
	pruned, err := dreamstate.Prune(opts.StateDir, runStart)
	if err != nil {
		return fmt.Errorf("state の掃除失敗: %w", err)
	}

	filter := &walkFilter{
		projectGlob: opts.ProjectGlob,
		incremental: opts.Incremental,
		lastRun:     state.LastRunTS,
	}

	var records []harvestRecord
	err = jsonlscan.WalkJSONLFiles(resolvedProjects, jsonlscan.WalkOptions{Days: opts.Days}, func(path string) error {
		if !filter.accept(path, resolvedProjects) {
			return nil
		}
		rec, err := scanSessionFile(path)
		if err != nil {
			return nil
		}
		if rec == nil {
			return nil
		}
		records = append(records, *rec)
		return nil
	})
	if err != nil {
		return fmt.Errorf("スキャン失敗: %w", err)
	}

	writeStats, err := writeRecords(opts.StateDir, records)
	if err != nil {
		return fmt.Errorf("書き込み失敗: %w", err)
	}

	state.LastRunTS = runStart.Format(time.RFC3339)
	if err := saveState(opts.StateDir, state); err != nil {
		return fmt.Errorf("state 保存失敗: %w", err)
	}

	if opts.Verbose {
		_, _ = fmt.Fprintf(opts.Stderr, "harvested=%d written=%d skipped=%d pruned=%d out=%s\n",
			len(records), writeStats.Written, writeStats.SkippedDuplicate, pruned, filepath.Join(opts.StateDir, "harvest"))
	}
	return nil
}
