package dreamio

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadCandidates_missingFile(t *testing.T) {
	got, err := ReadCandidates(filepath.Join(t.TempDir(), "nope.jsonl"))
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
	if err := WriteCandidates(path, want); err != nil {
		t.Fatalf("WriteCandidates: %v", err)
	}
	got, err := ReadCandidates(path)
	if err != nil {
		t.Fatalf("ReadCandidates: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestFindCandidateByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "candidates.jsonl")
	cands := []Candidate{
		{ID: "a", Kind: "skill_body"},
		{ID: "b", Kind: "memory_dedup"},
	}
	if err := WriteCandidates(path, cands); err != nil {
		t.Fatalf("WriteCandidates: %v", err)
	}

	got, err := FindCandidateByID(path, "b")
	if err != nil {
		t.Fatalf("FindCandidateByID: %v", err)
	}
	if got.ID != "b" || got.Kind != "memory_dedup" {
		t.Fatalf("got=%+v", got)
	}

	if _, err := FindCandidateByID(path, "missing"); err == nil {
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
