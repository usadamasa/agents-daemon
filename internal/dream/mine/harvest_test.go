package mine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHarvestFixture は harvest 出力を模した jsonl ファイルを書き出す。
func writeHarvestFixture(t *testing.T, dir, filename string, records []map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	var lines []string
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadHarvestForProject(t *testing.T) {
	dir := t.TempDir()
	writeHarvestFixture(t, dir, "2026-07-19.jsonl", []map[string]any{
		{"session_id": "s1", "cwd_project": "-Users-x-workspace", "cwd": "/Users/x/workspace",
			"skills_invoked": []string{"daily-standup"}, "n_messages": 5},
		{"session_id": "s2", "cwd_project": "-Users-x-other", "cwd": "/Users/x/other",
			"skills_invoked": []string{"skill-a"}, "n_messages": 3},
	})
	writeHarvestFixture(t, dir, "2026-07-20.jsonl", []map[string]any{
		{"session_id": "s3", "cwd_project": "-Users-x-workspace", "cwd": "/Users/x/workspace",
			"skills_invoked": []string{"daily-standup", "caddi-obsidian"}, "n_messages": 8},
	})

	got, err := loadHarvestForProject(dir, "-Users-x-workspace")
	if err != nil {
		t.Fatalf("loadHarvestForProject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions for project, got %d", len(got))
	}
	// session_id はどちらの順序でも許容 (ファイル名の walk 順序に依存)
	sids := map[string]bool{got[0].SessionID: true, got[1].SessionID: true}
	if !sids["s1"] || !sids["s3"] {
		t.Errorf("expected sessions s1 and s3, got %v", sids)
	}
}

func TestLoadHarvestForProject_MissingDirReturnsEmpty(t *testing.T) {
	got, err := loadHarvestForProject("/nonexistent/dir", "-Users-x-workspace")
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(got))
	}
}

func TestLoadHarvestForProject_ExtractsAllSkills(t *testing.T) {
	dir := t.TempDir()
	writeHarvestFixture(t, dir, "2026-07-19.jsonl", []map[string]any{
		{"session_id": "s1", "cwd_project": "-Users-x-workspace",
			"skills_invoked": []string{"daily-standup", "caddi-obsidian"}, "n_messages": 5},
		{"session_id": "s2", "cwd_project": "-Users-x-workspace",
			"skills_invoked": []string{"daily-standup", "weekly-report"}, "n_messages": 3},
	})
	recs, err := loadHarvestForProject(dir, "-Users-x-workspace")
	if err != nil {
		t.Fatal(err)
	}
	skills := extractInvokedSkills(recs)
	// deduped + sorted
	want := []string{"caddi-obsidian", "daily-standup", "weekly-report"}
	if len(skills) != len(want) {
		t.Fatalf("skills = %v; want %v", skills, want)
	}
	for i, s := range skills {
		if s != want[i] {
			t.Errorf("skills[%d] = %q; want %q", i, s, want[i])
		}
	}
}
