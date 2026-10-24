package harvest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadState_MissingFile(t *testing.T) {
	dir := t.TempDir()
	s, err := loadState(dir)
	if err != nil {
		t.Fatalf("loadState should return no error for missing file: %v", err)
	}
	if s == nil || s.LastRunTS != "" {
		t.Errorf("expected empty state, got %#v", s)
	}
}

func TestSaveAndLoadState_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	in := &harvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}
	if err := saveState(dir, in); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatalf("state file should exist: %v", err)
	}

	out, err := loadState(dir)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if out.LastRunTS != in.LastRunTS {
		t.Errorf("LastRunTS: got %q, want %q", out.LastRunTS, in.LastRunTS)
	}
}

func TestSaveState_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")
	if err := saveState(dir, &harvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}); err != nil {
		t.Fatalf("saveState should create the dir: %v", err)
	}
}

// 書き直しは一時ファイル経由なので、終わった後に state file 以外が残らないことを見る。
func TestSaveState_LeavesOnlyStateFile(t *testing.T) {
	dir := t.TempDir()
	for range 2 {
		if err := saveState(dir, &harvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}); err != nil {
			t.Fatalf("saveState: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != stateFileName {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir entries = %v, want [%s]", names, stateFileName)
	}
}

func TestParseLastRun(t *testing.T) {
	tests := []struct {
		name     string
		s        *harvestState
		wantZero bool
	}{
		{"nil", nil, true},
		{"empty ts", &harvestState{}, true},
		{"invalid ts", &harvestState{LastRunTS: "not-a-timestamp"}, true},
		{"valid ts", &harvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLastRun(tc.s)
			if got.IsZero() != tc.wantZero {
				t.Errorf("parseLastRun(%#v).IsZero() = %v; want %v", tc.s, got.IsZero(), tc.wantZero)
			}
		})
	}
}

// Test that state timestamps parse in the expected timezone (defense against RFC3339 vs 3339Nano drift)
func TestParseLastRun_MatchesRFC3339Format(t *testing.T) {
	want := time.Date(2026, 7, 19, 10, 0, 0, 0, time.FixedZone("JST", 9*3600))
	got := parseLastRun(&harvestState{LastRunTS: want.Format(time.RFC3339)})
	if !got.Equal(want) {
		t.Errorf("parseLastRun did not roundtrip: got %v, want %v", got, want)
	}
}
