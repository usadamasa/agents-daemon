package doctor

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// geometryOf は pane ごとに固定の Geometry を返す fake。無い pane は error。
func geometryOf(m map[string]multiplexer.Geometry) *multiplexerfake.Client {
	return &multiplexerfake.Client{
		PaneGeometryFunc: func(_ context.Context, paneID string) (multiplexer.Geometry, error) {
			g, ok := m[paneID]
			if !ok {
				return multiplexer.Geometry{}, errors.New("rex [block call] が失敗 (テスト)")
			}
			return g, nil
		},
	}
}

// pane 一覧は Claude の pane ごとに block の高さと screen を出し、高さが実測の下限 (detect.MinDetectableRows)
// 未満なら「検知できないことがある」と ⚠ を出す。取れなければ理由を出して pane の行は残す。
func TestReportPanes_Geometry(t *testing.T) {
	client := geometryOf(map[string]multiplexer.Geometry{
		"tall":  {Rows: 52, Columns: 139, AlternateScreen: true},
		"short": {Rows: detect.MinDetectableRows - 1, Columns: 139, AlternateScreen: true},
	})
	rep := collectPanes(context.Background(), client, []multiplexer.Pane{
		{PaneID: "tall", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
		{PaneID: "short", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
		{PaneID: "gone", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
		{PaneID: "shell", CWD: "/w", Agent: ""},
	})
	if len(rep.Claude) != 3 || rep.Claude[0].Geometry == nil || rep.Claude[0].Geometry.Rows != 52 {
		t.Fatalf("Claude = %+v", rep.Claude)
	}
	if rep.Claude[2].Geometry != nil || rep.Claude[2].GeometryError == "" {
		t.Errorf("取れない pane = %+v, want geometry 無しで理由あり", rep.Claude[2])
	}
	if strings.Contains(strings.Join(client.Calls, " "), "PaneGeometry:shell") {
		t.Errorf("Claude でない pane に size / modes を打っている: %v", client.Calls)
	}

	var buf bytes.Buffer
	writePanes(&buf, *rep)
	out := buf.String()
	for _, want := range []string{"rows=52", "screen=alternate", "rows=" + strconv.Itoa(detect.MinDetectableRows-1), "block call", "⚠"} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が無い:\n%s", want, out)
		}
	}
	if strings.Count(out, "⚠") != 1 {
		t.Errorf("⚠ は高さの足りない pane の 1 件だけのはず:\n%s", out)
	}
}

// doctor の pane 一覧は、record があるのに使えなかった理由を pane の行に添える。
// rex の版が変わって全 pane が unknown になったとき、ここが唯一の手がかりになる。
func TestReportPanes_UnknownReason(t *testing.T) {
	const reason = "record に owner.pid が無い (テスト)"
	var buf bytes.Buffer
	writePanes(&buf, *collectPanes(context.Background(), geometryOf(nil), []multiplexer.Pane{
		{PaneID: "b1", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusUnknown, UnknownReason: reason},
		{PaneID: "b2", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
	}))
	out := buf.String()
	if !strings.Contains(out, reason) {
		t.Errorf("理由が出ていない:\n%s", out)
	}
	if strings.Count(out, "理由") != 1 {
		t.Errorf("理由の無い pane にも理由の欄が出ている:\n%s", out)
	}
}

// record の kind と msg はあるときだけ pane の行に添える。
func TestReportPanes_RecordKindAndMessage(t *testing.T) {
	var buf bytes.Buffer
	writePanes(&buf, *collectPanes(context.Background(), geometryOf(nil), []multiplexer.Pane{
		{PaneID: "b1", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusBlocked, AgentKind: "permission", AgentMessage: "approve Bash: rm"},
		{PaneID: "b2", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking},
	}))
	out := buf.String()
	for _, want := range []string{"kind=permission", `msg="approve Bash: rm"`} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が無い:\n%s", want, out)
		}
	}
	if strings.Count(out, "kind=") != 1 {
		t.Errorf("kind の無い pane にも kind= が出ている:\n%s", out)
	}
}
