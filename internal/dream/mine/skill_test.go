package mine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkill_ReadsSkillMdAndReferences(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "daily-standup")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("# daily-standup\n\nBody of SKILL.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "classification.md"),
		[]byte("# Classification\n\nSlack filter rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "other.md"),
		[]byte("# Other reference\n\nMore rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sk, err := loadSkill(dir, "daily-standup")
	if err != nil {
		t.Fatalf("loadSkill: %v", err)
	}
	if sk.Name != "daily-standup" {
		t.Errorf("Name = %q", sk.Name)
	}
	if !strings.Contains(sk.Body, "Body of SKILL.md") {
		t.Errorf("body missing SKILL.md content")
	}
	if !strings.Contains(sk.Body, "Slack filter rules") {
		t.Errorf("body missing references/classification.md")
	}
	if !strings.Contains(sk.Body, "More rules") {
		t.Errorf("body missing references/other.md")
	}
}

func TestLoadSkill_MissingSkillReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := loadSkill(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
}

func TestLoadSkill_NoReferencesDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "no-refs-skill")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("just SKILL.md\n"), 0o644)

	sk, err := loadSkill(dir, "no-refs-skill")
	if err != nil {
		t.Fatalf("loadSkill: %v", err)
	}
	if !strings.Contains(sk.Body, "just SKILL.md") {
		t.Errorf("body missing SKILL.md")
	}
}

func TestLoadSkill_ResolvesSymlink(t *testing.T) {
	// symlink 実体を追ってその parent の references/ も読めるか確認
	realDir := t.TempDir()
	realSkill := filepath.Join(realDir, "real-skill")
	if err := os.MkdirAll(filepath.Join(realSkill, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, "SKILL.md"), []byte("real body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, "references", "r.md"), []byte("real ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkedDir := t.TempDir()
	if err := os.Symlink(realSkill, filepath.Join(linkedDir, "aliased-skill")); err != nil {
		t.Skip("symlink not supported on this fs")
	}

	sk, err := loadSkill(linkedDir, "aliased-skill")
	if err != nil {
		t.Fatalf("loadSkill: %v", err)
	}
	if !strings.Contains(sk.Body, "real body") || !strings.Contains(sk.Body, "real ref") {
		t.Errorf("symlink resolution failed: body=%q", sk.Body)
	}
}
