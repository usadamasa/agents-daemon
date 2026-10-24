package harvest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeAndDedup_LastWins(t *testing.T) {
	existing := []harvestRecord{
		{SessionID: "a", NMessages: 1},
		{SessionID: "b", NMessages: 2},
	}
	incoming := []harvestRecord{
		{SessionID: "b", NMessages: 5}, // overrides
		{SessionID: "c", NMessages: 3},
	}
	got := mergeAndDedup(existing, incoming)
	if len(got) != 3 {
		t.Fatalf("expected 3 unique sessions, got %d", len(got))
	}
	// sorted by session_id: a, b, c
	if got[0].SessionID != "a" || got[1].SessionID != "b" || got[2].SessionID != "c" {
		t.Errorf("unexpected order: %v", []string{got[0].SessionID, got[1].SessionID, got[2].SessionID})
	}
	if got[1].NMessages != 5 {
		t.Errorf("b should be last-wins with 5, got %d", got[1].NMessages)
	}
}

func TestWriteRecords_Idempotent(t *testing.T) {
	outDir := t.TempDir()
	records := []harvestRecord{
		{SessionID: "sess1", TSStart: "2026-07-19T10:00:00+09:00", NMessages: 5},
		{SessionID: "sess2", TSStart: "2026-07-19T11:00:00+09:00", NMessages: 3},
	}

	// First run
	if _, err := writeRecords(outDir, records); err != nil {
		t.Fatalf("first writeRecords: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(outDir, "harvest", "2026-07-19.jsonl"))
	if err != nil {
		t.Fatalf("read after first: %v", err)
	}

	// Second run — identical input
	if _, err := writeRecords(outDir, records); err != nil {
		t.Fatalf("second writeRecords: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(outDir, "harvest", "2026-07-19.jsonl"))
	if err != nil {
		t.Fatalf("read after second: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("output differs between identical runs (not idempotent)\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteRecords_DedupAcrossRuns(t *testing.T) {
	outDir := t.TempDir()
	initial := []harvestRecord{
		{SessionID: "sess1", TSStart: "2026-07-19T10:00:00+09:00", NMessages: 5},
	}
	if _, err := writeRecords(outDir, initial); err != nil {
		t.Fatalf("initial: %v", err)
	}

	// same session with updated NMessages (session ongoing)
	updated := []harvestRecord{
		{SessionID: "sess1", TSStart: "2026-07-19T10:00:00+09:00", NMessages: 20},
	}
	if _, err := writeRecords(outDir, updated); err != nil {
		t.Fatalf("updated: %v", err)
	}

	got := readAllRecords(t, filepath.Join(outDir, "harvest", "2026-07-19.jsonl"))
	if len(got) != 1 {
		t.Fatalf("expected 1 record after dedup, got %d", len(got))
	}
	if got[0].NMessages != 20 {
		t.Errorf("last-wins failed: NMessages=%d, want 20", got[0].NMessages)
	}
}

func TestWriteRecords_MultipleDates(t *testing.T) {
	outDir := t.TempDir()
	records := []harvestRecord{
		{SessionID: "sess1", TSStart: "2026-07-19T10:00:00+09:00", NMessages: 1},
		{SessionID: "sess2", TSStart: "2026-07-20T10:00:00+09:00", NMessages: 1},
	}
	if _, err := writeRecords(outDir, records); err != nil {
		t.Fatalf("writeRecords: %v", err)
	}

	for _, date := range []string{"2026-07-19", "2026-07-20"} {
		path := filepath.Join(outDir, "harvest", date+".jsonl")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}
	// 書き直しは一時ファイル経由なので、日付の jsonl 以外が残らないことを見る
	entries, err := os.ReadDir(filepath.Join(outDir, "harvest"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("harvest dir entries = %v, want 2 jsonl files", names)
	}
}

// records が無い run (incremental で新しいセッションが無いとき) は harvest/ を作らない。
// 読み手 (mine の WalkDir、dreamstate の Prune) は無いディレクトリを空として扱う。
func TestWriteRecords_NoRecordsLeavesNoDirectory(t *testing.T) {
	outDir := t.TempDir()
	if _, err := writeRecords(outDir, nil); err != nil {
		t.Fatalf("writeRecords(nil): %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "harvest")); !os.IsNotExist(err) {
		t.Errorf("harvest/ の Stat = %v, want not exist", err)
	}
}

func TestWriteRecords_SkipsCorruptedExistingLines(t *testing.T) {
	outDir := t.TempDir()
	harvestDir := filepath.Join(outDir, "harvest")
	if err := os.MkdirAll(harvestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(harvestDir, "2026-07-19.jsonl")
	corrupted := "{not json\n" + `{"session_id":"good","ts_start":"2026-07-19T10:00:00+09:00","n_messages":1}` + "\n"
	if err := os.WriteFile(dst, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	incoming := []harvestRecord{
		{SessionID: "new", TSStart: "2026-07-19T11:00:00+09:00", NMessages: 2},
	}
	if _, err := writeRecords(outDir, incoming); err != nil {
		t.Fatalf("writeRecords should tolerate corruption: %v", err)
	}
	got := readAllRecords(t, dst)
	ids := make([]string, 0, len(got))
	for _, r := range got {
		ids = append(ids, r.SessionID)
	}
	want := "good,new"
	joined := strings.Join(ids, ",")
	if joined != want {
		t.Errorf("expected ids=%q, got %q", want, joined)
	}
}

func readAllRecords(t *testing.T, path string) []harvestRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []harvestRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r harvestRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
