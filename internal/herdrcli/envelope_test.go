package herdrcli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		stderr  string
		wantID  string
		wantErr bool
	}{
		{
			name:   "stdout がそのまま JSON envelope",
			stdout: `{"id":"cli:pane:list","result":{"panes":[]}}`,
			wantID: "cli:pane:list",
		},
		{
			name: "stdout の前後にノイズ行が混ざる",
			stdout: strings.Join([]string{
				"Warning: something noisy",
				`{"id":"cli:pane:get","result":{"pane":{}}}`,
			}, "\n"),
			wantID: "cli:pane:get",
		},
		{
			name:   "envelope が stdout ではなく stderr に出る",
			stdout: "",
			stderr: `{"id":"cli:pane:read","result":{}}`,
			wantID: "cli:pane:read",
		},
		{
			name:   "stdout がノイズだけの場合は stderr にフォールバックする",
			stdout: "not json at all",
			stderr: `{"id":"cli:fallback","result":{}}`,
			wantID: "cli:fallback",
		},
		{
			name: "stdout の末尾行以外に { で始まる行があっても末尾から探す",
			stdout: strings.Join([]string{
				`{"this": "is not the envelope, just noise that happens to start with brace"}`,
				"some other noise",
				`{"id":"cli:tail","result":{}}`,
			}, "\n"),
			wantID: "cli:tail",
		},
		{
			name:    "stdout も stderr も空なら失敗する",
			stdout:  "",
			stderr:  "",
			wantErr: true,
		},
		{
			name:    "stdout も stderr も壊れた JSON なら失敗する",
			stdout:  "not json",
			stderr:  "also not json",
			wantErr: true,
		},
		{
			name:   "error フィールドを含む envelope も parse できる",
			stdout: `{"error":{"code":"pane_not_found","message":"pane x not found"},"id":"cli:pane:get"}`,
			wantID: "cli:pane:get",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := parseEnvelope([]byte(tt.stdout), []byte(tt.stderr))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseEnvelope() error = nil, wantErr true")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEnvelope() unexpected error: %v", err)
			}
			if env.ID != tt.wantID {
				t.Errorf("env.ID = %q, want %q", env.ID, tt.wantID)
			}
		})
	}
}

func TestParseEnvelope_ErrorFieldSurfaces(t *testing.T) {
	stdout := `{"error":{"code":"pane_not_found","message":"pane nonexistent-pane-id not found"},"id":"cli:request"}`
	env, err := parseEnvelope([]byte(stdout), nil)
	if err != nil {
		t.Fatalf("parseEnvelope() unexpected error: %v", err)
	}
	if env.Error == nil {
		t.Fatalf("env.Error = nil, want non-nil")
	}
	if env.Error.Code != "pane_not_found" {
		t.Errorf("env.Error.Code = %q, want %q", env.Error.Code, "pane_not_found")
	}
	wantMsg := "herdr: pane nonexistent-pane-id not found (pane_not_found)"
	if got := env.Error.Error(); got != wantMsg {
		t.Errorf("env.Error.Error() = %q, want %q", got, wantMsg)
	}
}

func TestParseEnvelope_RealFixtures(t *testing.T) {
	env, err := parseEnvelope([]byte(realPaneListFixture), nil)
	if err != nil {
		t.Fatalf("parseEnvelope() unexpected error: %v", err)
	}
	if env.ID != "cli:pane:list" {
		t.Errorf("env.ID = %q, want %q", env.ID, "cli:pane:list")
	}
	if env.Error != nil {
		t.Errorf("env.Error = %v, want nil", env.Error)
	}
	if !json.Valid(env.Result) {
		t.Errorf("env.Result が JSON として壊れている: %s", env.Result)
	}
}
