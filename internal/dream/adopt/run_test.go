package adopt

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

func TestRun_RejectsUnhomedCandidate(t *testing.T) {
	// unhomed は行き先が決まっていない候補｡branch を切る前に拒否する｡
	dir := t.TempDir()
	candFile := filepath.Join(dir, "candidates.jsonl")
	cands := []candidates.Candidate{
		{
			ID:         "dream-candidate-unhomed-feedback-random-001",
			Kind:       "unhomed",
			MemoryPath: "/mem/x.md",
			Confidence: "unknown",
		},
	}
	if err := candidates.Write(candFile, cands); err != nil {
		t.Fatalf("candidates.Write: %v", err)
	}

	_, err := Run(Opts{
		CandidateID:    cands[0].ID,
		CandidatesFile: candFile,
		DryRun:         true,
		Home:           dir,
	})
	if err == nil {
		t.Fatal("unhomed 候補が受理された; 拒否されるべき")
	}
	if !strings.Contains(err.Error(), "unhomed") {
		t.Errorf("エラーが理由を示していない: %v", err)
	}
}

// Run は stdout に出す手順と同じ中身を Plan で返す (--json が出す)。dry-run は branch を作らない。
func TestRun_ReturnsPlan(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	skill := filepath.Join(repo, "SKILL.md")
	if err := os.WriteFile(skill, []byte("# skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candFile := filepath.Join(t.TempDir(), "candidates.jsonl")
	cand := candidates.Candidate{
		ID:              "dream-candidate-skill-body-x-001",
		Kind:            candidates.KindSkillBody,
		SkillPath:       skill,
		MemoryPath:      "/mem/x.md",
		AssociatedSkill: "x",
		Confidence:      "high",
		Diagnosis:       "memory の内容が skill に無い",
	}
	if err := candidates.Write(candFile, []candidates.Candidate{cand}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	plan, err := Run(Opts{CandidateID: cand.ID, CandidatesFile: candFile, Date: "2026-10-31", DryRun: true, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(repo)
	if plan.CandidateID != cand.ID || plan.Kind != candidates.KindSkillBody || plan.Target != skill ||
		plan.RepoRoot != resolved || plan.Branch != "dream/2026-10-31-skill-body-x-001" || !plan.DryRun || plan.BranchCreated {
		t.Errorf("plan = %+v", plan)
	}
	if !strings.Contains(stdout.String(), plan.Branch) {
		t.Errorf("stdout に branch が無い: %q", stdout.String())
	}
}
