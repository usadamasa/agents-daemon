package stage

import (
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

// Run は --verbose が出すサマリと同じ数を Summary で返す (--json が出す)。
func TestRun_ReturnsSummary(t *testing.T) {
	dir := t.TempDir()
	candFile := filepath.Join(dir, "candidates.jsonl")
	if err := candidates.Write(candFile, []candidates.Candidate{{ID: "dream-candidate-x", Kind: candidates.KindSkillBody}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "report.md")
	sum, err := Run(Opts{Project: "-tmp-proj", CandidatesFile: candFile, OutFile: out})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Project != "-tmp-proj" || sum.Candidates != 1 || sum.Report != out {
		t.Errorf("summary = %+v", sum)
	}
}
