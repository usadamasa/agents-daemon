package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/dreamio"
)

func TestGenerateCandidates_PIIMigration(t *testing.T) {
	// coverage 0% ケース: PII rule が skill body に一切ない → skill_body high
	mems := []memory{
		{
			Name:        "feedback-standup-no-pii",
			Description: "PII を伏せる",
			Type:        "feedback",
			Body:        "**How to apply:** standup では面接候補者名を一次面接に一般化する｡",
			Path:        "/mem/feedback_standup_no_pii.md",
		},
	}
	skills := map[string]skill{
		"daily-standup": {Name: "daily-standup", Body: "SKILL body without PII rule"},
	}
	cands := generateCandidates(mems, skills, "-Users-x-workspace", []harvestSession{})
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	c := cands[0]
	if c.Kind != "skill_body" || c.Confidence != "high" {
		t.Errorf("Kind=%q Confidence=%q", c.Kind, c.Confidence)
	}
	// target 相当情報は AssociatedSkill + MemoryPath + SkillPath の 3 つ｡
	// Adopt phase が SkillPath (SKILL.md) を Edit 対象として使う｡
	if c.AssociatedSkill != "daily-standup" {
		t.Errorf("AssociatedSkill = %q; want daily-standup", c.AssociatedSkill)
	}
	if c.ScopedToProject != "-Users-x-workspace" {
		t.Errorf("ScopedToProject = %q", c.ScopedToProject)
	}
	if c.MemoryPath != "/mem/feedback_standup_no_pii.md" {
		t.Errorf("MemoryPath = %q", c.MemoryPath)
	}
}

func TestGenerateCandidates_FullyCoveredMemoryDedup(t *testing.T) {
	// coverage 100% ケース: memory 内容が skill body に完全 inline → memory_dedup high
	mems := []memory{
		{
			Name:        "feedback-standup-date-range",
			Description: "date range default",
			Type:        "feedback",
			Body:        "Done は前営業日 / Doing は当日",
			Path:        "/mem/feedback_standup_date_range.md",
		},
	}
	skills := map[string]skill{
		"daily-standup": {Name: "daily-standup", Body: "日付モデル: Done は前営業日 / Doing は当日"},
	}
	cands := generateCandidates(mems, skills, "-Users-x-workspace", nil)
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	c := cands[0]
	if c.Kind != "memory_dedup" || c.Confidence != "high" {
		t.Errorf("Kind=%q Confidence=%q", c.Kind, c.Confidence)
	}
}

func TestGenerateCandidates_UnhomedWhenNoAssociatedSkill(t *testing.T) {
	// association 先が無い memory も候補として出す｡skip すると stage レポートに現れず、
	// 行き先を決める機会が消える｡
	mems := []memory{
		{
			Name:        "feedback-random",
			Description: "unrelated to any skill",
			Type:        "feedback",
			Body:        "何かのフィードバック",
			Path:        "/mem/x.md",
		},
	}
	skills := map[string]skill{
		"daily-standup": {Name: "daily-standup", Body: "some body"},
	}
	cands := generateCandidates(mems, skills, "-p", nil)
	if len(cands) != 1 {
		t.Fatalf("expected 1 unhomed candidate, got %d: %+v", len(cands), cands)
	}
	c := cands[0]
	if c.Kind != "unhomed" {
		t.Errorf("Kind = %q; want unhomed", c.Kind)
	}
	if c.AssociatedSkill != "" {
		t.Errorf("AssociatedSkill = %q; want empty", c.AssociatedSkill)
	}
	if c.MemoryPath != "/mem/x.md" {
		t.Errorf("MemoryPath = %q", c.MemoryPath)
	}
	if !strings.Contains(c.Diagnosis, "feedback-random") {
		t.Errorf("Diagnosis に memory 名が入っていない: %q", c.Diagnosis)
	}
	if c.ID == "" || strings.Contains(c.ID, "--") {
		t.Errorf("ID = %q", c.ID)
	}
}

func TestGenerateCandidates_UnhomedIDsAreUniquePerMemory(t *testing.T) {
	mems := []memory{
		{Name: "feedback-alpha", Type: "feedback", Body: "a", Path: "/mem/a.md"},
		{Name: "feedback-beta", Type: "feedback", Body: "b", Path: "/mem/b.md"},
	}
	cands := generateCandidates(mems, map[string]skill{}, "-p", nil)
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(cands))
	}
	if cands[0].ID == cands[1].ID {
		t.Errorf("ID が重複している: %q", cands[0].ID)
	}
}

func TestGenerateCandidates_IncludesEvidenceSessions(t *testing.T) {
	mems := []memory{
		{
			Name: "feedback-standup-default-channel",
			Type: "feedback",
			Body: "デフォルトは #z-times-usadamasa に投稿",
			Path: "/mem/a.md",
		},
	}
	skills := map[string]skill{
		"daily-standup": {Name: "daily-standup", Body: "no channel default here"},
	}
	sessions := []harvestSession{
		{SessionID: "sess-a", SkillsInvoked: []string{"daily-standup"}, CWDProject: "-p"},
		{SessionID: "sess-b", SkillsInvoked: []string{"caddi-obsidian"}, CWDProject: "-p"},
		{SessionID: "sess-c", SkillsInvoked: []string{"daily-standup", "weekly-report"}, CWDProject: "-p"},
	}
	cands := generateCandidates(mems, skills, "-p", sessions)
	if len(cands) != 1 {
		t.Fatalf("candidates: %d", len(cands))
	}
	got := cands[0].EvidenceSessions
	// daily-standup を invoke したセッションだけ入る (sess-a, sess-c)
	if len(got) != 2 {
		t.Errorf("expected 2 evidence sessions (sess-a, sess-c), got %v", got)
	}
}

func TestWriteCandidates_JsonlFormat(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "candidates.jsonl")
	cands := []dreamio.Candidate{
		{
			ID:               "dream-candidate-daily-standup-001",
			Kind:             "skill_body",
			AssociatedSkill:  "daily-standup",
			MemoryPath:       "/mem/a.md",
			ScopedToProject:  "-Users-x-workspace",
			Diagnosis:        "PII rule not inlined",
			EvidenceSessions: []string{"sess1", "sess2"},
			Confidence:       "high",
			CoverageRatio:    0.0,
		},
	}
	if err := dreamio.WriteCandidates(outFile, cands); err != nil {
		t.Fatalf("dreamio.WriteCandidates: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var got dreamio.Candidate
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != cands[0].ID {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Kind != "skill_body" {
		t.Errorf("Kind = %q", got.Kind)
	}
	if len(got.EvidenceSessions) != 2 {
		t.Errorf("EvidenceSessions = %v", got.EvidenceSessions)
	}
}

func TestBuildCandidateID_Deterministic(t *testing.T) {
	id1 := buildCandidateID("daily-standup", 1)
	if id1 != "dream-candidate-daily-standup-001" {
		t.Errorf("id1 = %q", id1)
	}
	id2 := buildCandidateID("caddi-obsidian", 42)
	if id2 != "dream-candidate-caddi-obsidian-042" {
		t.Errorf("id2 = %q", id2)
	}
}
