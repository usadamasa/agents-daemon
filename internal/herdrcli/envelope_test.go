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
	// 実機の herdr から採取した出力そのもの (cwd などは採取時の値のまま残す)｡
	paneListFixture := `{"id":"cli:pane:list","result":{"panes":[{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"0d5e717f-82c5-4e71-ac74-799f7197e0de"},"agent_status":"working","cwd":"/Users/usadamasa/src/github.com/usadamasa/claude-config","focused":true,"foreground_cwd":"/Users/usadamasa/src/github.com/usadamasa/claude-config","pane_id":"wH:p1","revision":76,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"wH:t1","terminal_id":"term_6590ddf454a3a1","terminal_title":"title","terminal_title_stripped":"title","workspace_id":"wH"},{"agent_status":"unknown","cwd":"/x","focused":false,"foreground_cwd":"/x","pane_id":"wH:p4","revision":5,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"wH:t4","terminal_id":"term_6591d9bed8d704","terminal_title":"shell","terminal_title_stripped":"shell","workspace_id":"wH"}],"type":"pane_list"}}`

	env, err := parseEnvelope([]byte(paneListFixture), nil)
	if err != nil {
		t.Fatalf("parseEnvelope() unexpected error: %v", err)
	}
	var res paneListResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("result unmarshal: %v", err)
	}
	if len(res.Panes) != 2 {
		t.Fatalf("len(res.Panes) = %d, want 2", len(res.Panes))
	}
	claudePane := res.Panes[0]
	if !IsClaudeAgent(claudePane) {
		t.Errorf("IsClaudeAgent(claudePane) = false, want true")
	}
	if claudePane.AgentSession == nil || claudePane.AgentSession.Value != "0d5e717f-82c5-4e71-ac74-799f7197e0de" {
		t.Errorf("claudePane.AgentSession = %+v, want session_id 0d5e717f-...", claudePane.AgentSession)
	}
	if claudePane.AgentStatus != AgentStatusWorking {
		t.Errorf("claudePane.AgentStatus = %q, want %q", claudePane.AgentStatus, AgentStatusWorking)
	}

	shellPane := res.Panes[1]
	if IsClaudeAgent(shellPane) {
		t.Errorf("IsClaudeAgent(shellPane) = true, want false (agent フィールドが無い)")
	}
	if shellPane.AgentSession != nil {
		t.Errorf("shellPane.AgentSession = %+v, want nil", shellPane.AgentSession)
	}
	if shellPane.AgentStatus != AgentStatusUnknown {
		t.Errorf("shellPane.AgentStatus = %q, want %q", shellPane.AgentStatus, AgentStatusUnknown)
	}
}
