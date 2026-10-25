package candidates

import (
	"path/filepath"
	"testing"
)

func TestFilename(t *testing.T) {
	tests := []struct {
		name    string
		project string
		date    string
		want    string
	}{
		{
			name:    "canonical with leading dash",
			project: "-Users-x-workspace",
			date:    "2026-07-20",
			want:    "Users-x-workspace-2026-07-20.jsonl",
		},
		{
			name:    "no leading dash",
			project: "some-project",
			date:    "2026-01-01",
			want:    "some-project-2026-01-01.jsonl",
		},
		{
			name:    "empty project",
			project: "",
			date:    "2026-07-20",
			want:    "-2026-07-20.jsonl",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filename(tc.project, tc.date)
			if got != tc.want {
				t.Errorf("filename(%q, %q) = %q; want %q",
					tc.project, tc.date, got, tc.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	got := Path("/state", "-Users-x-workspace", "2026-07-20")
	want := filepath.Join("/state", "candidates", "Users-x-workspace-2026-07-20.jsonl")
	if got != want {
		t.Errorf("Path = %q; want %q", got, want)
	}
}
