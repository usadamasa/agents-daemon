// Package mine は dream 7-phase pipeline の Phase 2 Mine を提供する。
//
// harvest の出力と対象 project の feedback memory・skill 本体を
// 突き合わせて、skill 本体反映の migration 候補と重複 memory 削除候補を
// candidates.jsonl として抽出する。
package mine

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/harvest/record"
	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
	"github.com/usadamasa/agents-daemon/internal/dream/mine/corpus"
)

// Stderr は cobra 側で必ず inject されるが、テストや直呼び用に nil 時は Discard に fallback する。

// Opts は mine サブコマンドの実行パラメータ。
type Opts struct {
	Project    string // "" なら Cwd から canonicalize
	StateDir   string // dream の state ディレクトリ (dream.Dir)。必須
	HarvestDir string // 空なら StateDir/harvest
	MemoryDir  string // 空なら ~/.claude/projects/<project>/memory
	SkillsDir  string // 空なら ~/.claude/skills
	Date       string // 空なら今日
	Verbose    bool

	Home   string
	Cwd    string
	Stderr io.Writer
}

// Run は mine phase 一回分を実行する。
func Run(opts Opts) error {
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.StateDir == "" {
		return errors.New("StateDir が未解決 (呼び出し側が apppath で決める)")
	}

	project, err := dream.ResolveProject(opts.Project, opts.Cwd)
	if err != nil {
		return fmt.Errorf("project 解決失敗: %w", err)
	}

	resolvedHarvest := opts.HarvestDir
	if resolvedHarvest == "" {
		resolvedHarvest = filepath.Join(opts.StateDir, "harvest")
	}
	resolvedMemory := opts.MemoryDir
	if resolvedMemory == "" {
		resolvedMemory = filepath.Join(opts.Home, ".claude", "projects", project, "memory")
	}
	resolvedSkills := opts.SkillsDir
	if resolvedSkills == "" {
		resolvedSkills = filepath.Join(opts.Home, ".claude", "skills")
	}
	resolvedDate := dream.ResolveDate(opts.Date)

	// 1. harvest: 対象 project のセッション
	sessions, err := record.LoadForProject(resolvedHarvest, project)
	if err != nil {
		return fmt.Errorf("harvest 読み込み失敗: %w", err)
	}

	// 2. 対象 skill セット: セッションで invoked された skill をベースに
	invokedSkills := extractInvokedSkills(sessions)

	// 3. 各 skill の実体を読み込む。
	// 探索順: global → project-local (daily-standup など project 固有 skill を拾う)
	skillDirs := []string{resolvedSkills}
	if projRoot := projectRootFromSessions(sessions); projRoot != "" {
		skillDirs = append(skillDirs, filepath.Join(projRoot, ".claude", "skills"))
	}
	skills := make(map[string]corpus.Skill)
	for _, name := range invokedSkills {
		s, err := corpus.LoadSkillFromDirs(skillDirs, name)
		if err != nil {
			// plugin 由来 (e.g., commit-commands:commit-push-pr) や外部 skill は skip
			continue
		}
		skills[name] = s
	}

	// 4. memory: 対象 project の feedback memory
	mems, err := corpus.LoadMemories(resolvedMemory)
	if err != nil {
		return fmt.Errorf("memory 読み込み失敗: %w", err)
	}

	// 5. candidate 生成
	cands := generateCandidates(mems, skills, project, sessions)

	// 6. jsonl 出力
	outFile := candidates.Path(opts.StateDir, project, resolvedDate)
	if err := candidates.Write(outFile, cands); err != nil {
		return fmt.Errorf("candidates 書き込み失敗: %w", err)
	}

	if opts.Verbose {
		skillBody, memoryDedup, unhomed := candidates.CountByKind(cands)
		_, _ = fmt.Fprintf(opts.Stderr,
			"project=%s sessions=%d skills=%d memories=%d candidates=%d (skill_body=%d memory_dedup=%d unhomed=%d) out=%s\n",
			project, len(sessions), len(skills), len(mems), len(cands), skillBody, memoryDedup, unhomed, outFile)
	}
	return nil
}
