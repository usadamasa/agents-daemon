package herdrcli

import (
	"encoding/json"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// realPaneListFixture は実機の herdr から採取した `pane list` の出力そのもの
// (cwd などは採取時の値のまま残す)｡
const realPaneListFixture = `{"id":"cli:pane:list","result":{"panes":[{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"0d5e717f-82c5-4e71-ac74-799f7197e0de"},"agent_status":"working","cwd":"/Users/usadamasa/src/github.com/usadamasa/claude-config","focused":true,"foreground_cwd":"/Users/usadamasa/src/github.com/usadamasa/claude-config","pane_id":"wH:p1","revision":76,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"wH:t1","terminal_id":"term_6590ddf454a3a1","terminal_title":"title","terminal_title_stripped":"title","workspace_id":"wH"},{"agent_status":"unknown","cwd":"/x","focused":false,"foreground_cwd":"/x","pane_id":"wH:p4","revision":5,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"wH:t4","terminal_id":"term_6591d9bed8d704","terminal_title":"shell","terminal_title_stripped":"shell","workspace_id":"wH"}],"type":"pane_list"}}`

func TestPaneWire_ToPane_RealFixture(t *testing.T) {
	env, err := parseEnvelope([]byte(realPaneListFixture), nil)
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

	want := []multiplexer.Pane{
		{
			PaneID:        "wH:p1",
			TerminalID:    "term_6590ddf454a3a1",
			CWD:           "/Users/usadamasa/src/github.com/usadamasa/claude-config",
			Agent:         "claude",
			AgentStatus:   multiplexer.AgentStatusWorking,
			SessionID:     "0d5e717f-82c5-4e71-ac74-799f7197e0de",
			TerminalTitle: "title",
		},
		{
			// agent_session が無い素の shell pane は SessionID が空になる
			PaneID:        "wH:p4",
			TerminalID:    "term_6591d9bed8d704",
			CWD:           "/x",
			AgentStatus:   multiplexer.AgentStatusUnknown,
			TerminalTitle: "shell",
		},
	}
	for i, w := range res.Panes {
		if got := w.toPane(); got != want[i] {
			t.Errorf("panes[%d].toPane() = %+v, want %+v", i, got, want[i])
		}
	}
}
