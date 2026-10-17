package verify

import (
	"bytes"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// doctor の pane 一覧は、record があるのに使えなかった理由を pane の行に添える。
// rex の版が変わって全 pane が unknown になったとき、ここが唯一の手がかりになる。
func TestReportPanes_UnknownReason(t *testing.T) {
	const reason = "record に owner.pid が無い (テスト)"
	var buf bytes.Buffer
	reportPanes(&buf, []multiplexer.Pane{
		{PaneID: "b1", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusUnknown, UnknownReason: reason},
		{PaneID: "b2", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
	})
	out := buf.String()
	if !strings.Contains(out, reason) {
		t.Errorf("理由が出ていない:\n%s", out)
	}
	if strings.Count(out, "理由") != 1 {
		t.Errorf("理由の無い pane にも理由の欄が出ている:\n%s", out)
	}
}
