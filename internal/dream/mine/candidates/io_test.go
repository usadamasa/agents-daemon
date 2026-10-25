package candidates

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRead_missingFile(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %d", len(got))
	}
}

func TestWriteThenRead_roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "candidates.jsonl")

	want := []Candidate{
		{
			ID:               "dream-candidate-foo-001",
			Kind:             "skill_body",
			AssociatedSkill:  "foo",
			MemoryPath:       "/tmp/memory/foo.md",
			SkillPath:        "/tmp/skills/foo/SKILL.md",
			ScopedToProject:  "-Users-x-workspace",
			Diagnosis:        "coverage low",
			EvidenceSessions: []string{"s1", "s2"},
			Confidence:       "high",
			CoverageRatio:    0.25,
		},
		{
			ID:              "dream-candidate-bar-001",
			Kind:            "memory_dedup",
			AssociatedSkill: "bar",
			MemoryPath:      "/tmp/memory/bar.md",
			ScopedToProject: "-Users-x-workspace",
			Diagnosis:       "covered",
			Confidence:      "medium",
			CoverageRatio:   0.95,
		},
	}
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestFindByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "candidates.jsonl")
	cands := []Candidate{
		{ID: "a", Kind: "skill_body"},
		{ID: "b", Kind: "memory_dedup"},
	}
	if err := Write(path, cands); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := FindByID(path, "b")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != "b" || got.Kind != "memory_dedup" {
		t.Fatalf("got=%+v", got)
	}

	if _, err := FindByID(path, "missing"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestCountByKind(t *testing.T) {
	cands := []Candidate{
		{Kind: "skill_body"},
		{Kind: "memory_dedup"},
		{Kind: "skill_body"},
		{Kind: "unhomed"},
		{Kind: "unknown"},
	}
	sb, md, uh := CountByKind(cands)
	if sb != 2 || md != 1 || uh != 1 {
		t.Errorf("CountByKind = (%d, %d, %d); want (2, 1, 1)", sb, md, uh)
	}
}
