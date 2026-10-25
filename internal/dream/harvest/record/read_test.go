package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture は harvest 出力を模した jsonl ファイルを書き出す。
func writeFixture(t *testing.T, dir, filename string, records []map[string]any) {
	t.Helper()
	var lines []string
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadForProject(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "2026-07-19.jsonl", []map[string]any{
		{"session_id": "s1", "cwd_project": "-Users-x-workspace", "cwd": "/Users/x/workspace",
			"skills_invoked": []string{"daily-standup"}, "n_messages": 5},
		{"session_id": "s2", "cwd_project": "-Users-x-other", "cwd": "/Users/x/other",
			"skills_invoked": []string{"skill-a"}, "n_messages": 3},
	})
	writeFixture(t, dir, "2026-07-20.jsonl", []map[string]any{
		{"session_id": "s3", "cwd_project": "-Users-x-workspace", "cwd": "/Users/x/workspace",
			"skills_invoked": []string{"daily-standup", "caddi-obsidian"}, "n_messages": 8},
	})

	got, err := LoadForProject(dir, "-Users-x-workspace")
	if err != nil {
		t.Fatalf("LoadForProject: %v", err)
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

func TestLoadForProject_MissingDirReturnsEmpty(t *testing.T) {
	got, err := LoadForProject("/nonexistent/dir", "-Users-x-workspace")
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(got))
	}
}

// Write が書いたものを LoadForProject が同じ形で読み戻す。書き手と読み手が同じ型を使う理由。
func TestLoadForProject_ReadsWhatWriteWrote(t *testing.T) {
	outDir := t.TempDir()
	in := Record{
		SessionID: "s1", TSStart: "2026-07-19T10:00:00+09:00", CWDProject: "-p", NMessages: 2,
		SkillsInvoked:     []string{"a"},
		FeedbackSignals:   []FeedbackSignal{{Quote: "違う", Kind: "correction"}},
		FailedInvocations: []FailedInvocation{{InvokedSkill: "a", UserReaction: "違う"}},
	}
	if _, err := Write(outDir, []Record{in}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := LoadForProject(filepath.Join(outDir, "harvest"), "-p")
	if err != nil || len(got) != 1 {
		t.Fatalf("LoadForProject = %v, %v; want 1 record", got, err)
	}
	if got[0].FeedbackSignals[0] != in.FeedbackSignals[0] || got[0].FailedInvocations[0] != in.FailedInvocations[0] {
		t.Errorf("読み戻した record = %+v, want %+v", got[0], in)
	}
}
