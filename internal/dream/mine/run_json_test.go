package mine

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

// Run は --verbose が出すサマリと同じ数を Summary で返す (--json が出す)。材料が空なら候補も 0。
func TestRun_ReturnsSummary(t *testing.T) {
	stateDir := t.TempDir()
	sum, err := Run(Opts{
		Project:    "-tmp-proj",
		StateDir:   stateDir,
		HarvestDir: t.TempDir(),
		MemoryDir:  t.TempDir(),
		SkillsDir:  t.TempDir(),
		Date:       "2026-10-31",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Project != "-tmp-proj" || sum.Candidates != 0 || sum.Out != candidates.Path(stateDir, "-tmp-proj", "2026-10-31") {
		t.Errorf("summary = %+v", sum)
	}
}
