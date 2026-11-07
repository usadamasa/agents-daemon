package spawn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubagentModel(t *testing.T) {
	cases := []struct {
		name     string
		settings *string // nil なら settings.json を置かない
		want     string
		wantErr  string
	}{
		{name: "env に定義がある", settings: ptr(`{"env":{"CLAUDE_CODE_SUBAGENT_MODEL":"opus","OTHER":"x"}}`), want: "opus"},
		{name: "前後の空白は落とす", settings: ptr(`{"env":{"CLAUDE_CODE_SUBAGENT_MODEL":" sonnet "}}`), want: "sonnet"},
		{name: "settings.json が無い", settings: nil, want: ""},
		{name: "env にキーが無い", settings: ptr(`{"env":{"OTHER":"x"}}`), want: ""},
		{name: "env が無い", settings: ptr(`{"model":"opus"}`), want: ""},
		{name: "JSON が壊れている", settings: ptr(`{"env":`), wantErr: "settings.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.settings != nil {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(*tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := SubagentModel(dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q を含む", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
