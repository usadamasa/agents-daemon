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

// --project は mine と同じく絶対パスも受け、canonical name に直してから candidates を引く (#703)。
func TestRun_絶対パスの_projectをcanonical_nameに直す(t *testing.T) {
	stateDir := t.TempDir()
	if err := candidates.Write(candidates.Path(stateDir, "-tmp-fxproj", "2026-11-21"), []candidates.Candidate{
		{ID: "dream-candidate-x", Kind: candidates.KindSkillBody},
	}); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sum, err := Run(Opts{Project: "/tmp/fxproj", Date: "2026-11-21", StateDir: stateDir, Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Project != "-tmp-fxproj" || sum.Candidates != 1 || sum.Report != reportPath(cwd, "-tmp-fxproj", "2026-11-21") {
		t.Errorf("summary = %+v", sum)
	}
}

// canonical name でも絶対パスでもない --project は、受け付ける形を示してエラーで止まる。
func TestRun_形の違う_projectを拒む(t *testing.T) {
	if _, err := Run(Opts{Project: "tmp/fxproj", StateDir: t.TempDir(), Cwd: t.TempDir()}); err == nil {
		t.Fatal("相対パスの --project を受け付けた")
	}
}
