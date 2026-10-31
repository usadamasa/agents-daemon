// Package adopt は dream 7-phase pipeline の Phase 7 Adopt を提供する。
//
// candidate ID を受け取り以下を行う:
//  1. candidates.jsonl から candidate 情報を取得
//  2. skill_path/memory_path の symlink 実体をたどり、実際に commit する repo root を判定
//  3. その repo で作業ブランチ dream/YYYY-MM-DD-<slug> を作成
//  4. LLM (Claude Code interactive) が Edit + commit するための指示文を stdout に出力
//
// SKILL.md への Edit と git commit は本 pkg では行わない。push は user 明示指示まで保留。
package adopt

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

// Opts は adopt サブコマンドの実行パラメータ。
type Opts struct {
	CandidateID    string // 必須
	CandidatesFile string // 空なら StateDir/candidates/<slug>-<date>.jsonl
	Project        string // CandidatesFile 未指定時に必要
	Date           string
	DryRun         bool
	StateDir       string // dream の state ディレクトリ (dream.Dir)。CandidatesFile 未指定時に必要

	Home   string
	Stdout io.Writer
	Stderr io.Writer
}

// Plan は adopt が準備した作業の中身。stdout の手順と同じものを持ち、`--json` はそのまま出す。
type Plan struct {
	CandidateID     string `json:"candidate_id"`
	Kind            string `json:"kind"`
	Confidence      string `json:"confidence"`
	AssociatedSkill string `json:"associated_skill"`
	Diagnosis       string `json:"diagnosis"`
	MemoryPath      string `json:"memory_path"`
	Target          string `json:"target"`
	RepoRoot        string `json:"repo_root"`
	Branch          string `json:"branch"`
	DryRun          bool   `json:"dry_run"`
	// BranchCreated は Branch を作ったか。DryRun では作らない。
	BranchCreated bool `json:"branch_created"`
}

// Run は adopt phase 一回分を実行する。
func Run(opts Opts) (Plan, error) {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.CandidateID == "" {
		return Plan{}, fmt.Errorf("--candidate は必須")
	}

	resolvedDate := dream.ResolveDate(opts.Date)
	resolvedCandidates := opts.CandidatesFile
	if resolvedCandidates == "" {
		if opts.Project == "" {
			return Plan{}, fmt.Errorf("--candidates 未指定時は --project (canonical name) が必要")
		}
		if opts.StateDir == "" {
			return Plan{}, fmt.Errorf("StateDir が未解決 (呼び出し側が apppath で決める)")
		}
		resolvedCandidates = candidates.Path(opts.StateDir, opts.Project, resolvedDate)
	}

	cand, err := candidates.FindByID(resolvedCandidates, opts.CandidateID)
	if err != nil {
		return Plan{}, fmt.Errorf("候補読み込み失敗: %w", err)
	}

	if cand.Kind == candidates.KindUnhomed {
		return Plan{}, fmt.Errorf("候補 %s は行き先未定 (unhomed)。dream skill の行き先テーブルで行き先を決めてから adopt する", cand.ID)
	}

	targetPath := cand.SkillPath
	if cand.Kind == candidates.KindMemoryDedup {
		// memory_dedup は auto memory を削除する候補。Adopt の対象は memory ファイル自体。
		// auto memory は ~/.claude/projects/<proj>/memory/ にあり git 管理外の可能性が高い。
		targetPath = cand.MemoryPath
	}
	if targetPath == "" {
		return Plan{}, fmt.Errorf("候補に skill_path も memory_path も無い")
	}

	repoRoot, err := findRepoRoot(targetPath)
	if err != nil {
		_, _ = fmt.Fprintln(opts.Stderr, "→ このパスは git 管理外の可能性 (~/.claude/projects/memory/ 等)。")
		_, _ = fmt.Fprintln(opts.Stderr, "  手動バックアップ + Edit で対応してください。")
		return Plan{}, fmt.Errorf("repo 判定失敗 (target=%s): %w", targetPath, err)
	}
	plan := Plan{
		CandidateID: cand.ID, Kind: cand.Kind, Confidence: cand.Confidence, AssociatedSkill: cand.AssociatedSkill,
		Diagnosis: cand.Diagnosis, MemoryPath: cand.MemoryPath,
		Target: targetPath, RepoRoot: repoRoot, Branch: branchName(resolvedDate, cand.ID), DryRun: opts.DryRun,
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(opts.Stdout, "# dry-run: 実行されるコマンド\n\n")
	}
	printAdoptPlan(opts.Stdout, plan)

	if opts.DryRun {
		return plan, nil
	}

	if err := createBranch(repoRoot, plan.Branch); err != nil {
		return plan, fmt.Errorf("branch 作成失敗: %w", err)
	}
	plan.BranchCreated = true
	_, _ = fmt.Fprintf(opts.Stderr, "\n✓ branch %q を %s で作成しました。\n", plan.Branch, repoRoot)
	return plan, nil
}

// printAdoptPlan は user (と LLM) 向けの実行手順を stdout に出す。
func printAdoptPlan(w io.Writer, plan Plan) {
	pf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

	pf("## Adopt 準備: %s\n\n", plan.CandidateID)
	pf("- kind: %s\n", plan.Kind)
	pf("- confidence: %s\n", plan.Confidence)
	pf("- associated_skill: %s\n", plan.AssociatedSkill)
	pf("- target file: %s\n", plan.Target)
	pf("- repo root: %s\n", plan.RepoRoot)
	pf("- 作業ブランチ: %s\n\n", plan.Branch)
	pf("### Diagnosis\n\n%s\n\n", plan.Diagnosis)

	pf("### 次に user (or LLM) がすること\n\n")
	if plan.DryRun {
		pf("1. `git -C %s switch -c %s` (branch 作成)\n", plan.RepoRoot, plan.Branch)
	} else {
		pf("1. (完了) branch %s を作成済み\n", plan.Branch)
	}

	switch plan.Kind {
	case candidates.KindSkillBody:
		pf("2. `%s` を編集し、memory `%s` の内容を反映\n", plan.Target, plan.MemoryPath)
	case candidates.KindMemoryDedup:
		pf("2. `%s` を削除 or `deprecated_by:` frontmatter を追加\n", plan.Target)
	default:
		pf("2. 候補の kind に応じて `%s` を編集\n", plan.Target)
	}
	pf("3. diff レビュー後、`git -C %s add ...` → `git -C %s commit` で確定\n", plan.RepoRoot, plan.RepoRoot)
	pf("4. push は user 明示指示まで保留 (`git push` は自動しない)\n\n")

	pf("### memory 側の参考文言\n\n(memory_path=%s の中身を Edit の材料にする)\n", plan.MemoryPath)
}

// createBranch は repoRoot で `git switch -c <branch>` を実行する。
func createBranch(repoRoot, branch string) error {
	// #nosec G204 -- repoRoot は findRepoRoot 経由の git rev-parse 出力、
	// branch は buildCandidateID から派生した文字列で shell 特殊文字を含まない。
	cmd := exec.Command("git", "-C", repoRoot, "switch", "-c", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git switch failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
