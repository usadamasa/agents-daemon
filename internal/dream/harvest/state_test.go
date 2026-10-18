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
	in := &HarvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}
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
	if err := saveState(dir, &HarvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}); err != nil {
		t.Fatalf("saveState should create the dir: %v", err)
	}
}

func TestParseLastRun(t *testing.T) {
	tests := []struct {
		name     string
		s        *HarvestState
		wantZero bool
	}{
		{"nil", nil, true},
		{"empty ts", &HarvestState{}, true},
		{"invalid ts", &HarvestState{LastRunTS: "not-a-timestamp"}, true},
		{"valid ts", &HarvestState{LastRunTS: "2026-07-19T10:00:00+09:00"}, false},
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
	got := parseLastRun(&HarvestState{LastRunTS: want.Format(time.RFC3339)})
	if !got.Equal(want) {
		t.Errorf("parseLastRun did not roundtrip: got %v, want %v", got, want)
	}
}
