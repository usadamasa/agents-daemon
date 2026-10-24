package mine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSkillFromDirs_FindsInFirstDir(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	skillDir := filepath.Join(dir1, "skill-a")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("dir1 skill-a\n"), 0o644)

	sk, err := loadSkillFromDirs([]string{dir1, dir2}, "skill-a")
	if err != nil {
		t.Fatalf("loadSkillFromDirs: %v", err)
	}
	if sk.Body == "" || sk.Body == "\n" {
		t.Errorf("body empty: %q", sk.Body)
	}
}

func TestLoadSkillFromDirs_FallsBackToSecondDir(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	// skill-b は dir2 のみ
	skillDir := filepath.Join(dir2, "skill-b")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("dir2 skill-b\n"), 0o644)

	sk, err := loadSkillFromDirs([]string{dir1, dir2}, "skill-b")
	if err != nil {
		t.Fatalf("loadSkillFromDirs: %v", err)
	}
	if sk.RealPath == "" {
		t.Errorf("RealPath empty")
	}
}

func TestLoadSkillFromDirs_NotFoundInAny(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	_, err := loadSkillFromDirs([]string{dir1, dir2}, "missing-skill")
	if err == nil {
		t.Fatal("expected error when skill not in any dir")
	}
}

func TestProjectRootFromSessions_PicksFirstCWD(t *testing.T) {
	sessions := []harvestSession{
		{SessionID: "s1", CWD: "/Users/x/workspace"},
		{SessionID: "s2", CWD: "/Users/x/other"},
	}
	got := projectRootFromSessions(sessions)
	if got != "/Users/x/workspace" {
		t.Errorf("got %q; want /Users/x/workspace", got)
	}
}

func TestProjectRootFromSessions_EmptyReturnsEmpty(t *testing.T) {
	if got := projectRootFromSessions(nil); got != "" {
		t.Errorf("got %q; want ''", got)
	}
}
