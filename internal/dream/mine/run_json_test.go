package mine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/harvest/record"
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

// 規約の skillDirs は既定の置き場の後に走査し、規約の接頭辞で memory 名と結ぶ。
func TestRun_UsesPolicySkillDirsAndPrefixes(t *testing.T) {
	stateDir := t.TempDir()
	if _, err := record.Write(stateDir, []record.Record{
		{SessionID: "s1", CWDProject: "-tmp-proj", SkillsInvoked: []string{"team-notes"}, NMessages: 2},
	}); err != nil {
		t.Fatal(err)
	}
	extra := t.TempDir()
	writeTestFile(t, filepath.Join(extra, "team-notes", "SKILL.md"), "# team-notes\n\nメモの書き方\n")
	memDir := t.TempDir()
	writeTestFile(t, filepath.Join(memDir, "feedback_notes.md"),
		"---\nname: feedback-notes-style\ndescription: notes の書き方\nmetadata:\n  type: feedback\n---\n\n見出しは短くする\n")

	sum, err := Run(Opts{
		Project:           "-tmp-proj",
		StateDir:          stateDir,
		MemoryDir:         memDir,
		SkillsDir:         t.TempDir(),
		ExtraSkillDirs:    []string{extra},
		SkillNamePrefixes: []string{"team"},
		Date:              "2026-10-31",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skills != 1 || sum.Candidates != 1 || sum.Unhomed != 0 {
		t.Errorf("summary = %+v; want the policy skill loaded and the memory associated", sum)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
