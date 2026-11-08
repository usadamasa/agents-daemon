package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// writeClaudeConfig は Claude Code の設定ディレクトリの形で、登録簿 (sessions/<pid>.json) と transcript
// (projects/<project>/<sid>.jsonl) を一時ディレクトリに置く。
func writeClaudeConfig(t *testing.T, registry map[int]string, transcripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for pid, sid := range registry {
		path := filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json")
		writeFile(t, path, `{"pid":`+strconv.Itoa(pid)+`,"sessionId":"`+sid+`","name":"peer"}`)
	}
	for sid, line := range transcripts {
		writeFile(t, filepath.Join(dir, "projects", "-work", sid+".jsonl"), line+"\n")
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// transcript の末尾の entry。limitedEntry の解除は 2026-10-04T12:30:00Z、weeklyEntry の解除は 2026-10-07T04:00:00Z。
const (
	limitedEntry = `{"type":"assistant","uuid":"u-limit","timestamp":"2026-10-04T08:32:13.884Z","message":{"role":"assistant","content":[{"type":"text","text":"You've hit your session limit"}]},"quotaLimits":{"status":"rejected","resetsAt":1791117000,"rateLimitType":"five_hour"},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429,"version":"2.1.289"}`
	weeklyEntry  = `{"type":"assistant","uuid":"u-weekly","timestamp":"2026-10-04T08:40:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"You've hit your weekly limit · progress saved"}]},"quotaLimits":{"status":"rejected","resetsAt":1791345600,"rateLimitType":"seven_day"},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429,"version":"2.1.289"}`
	idleEntry    = `{"type":"user","uuid":"u-user","timestamp":"2026-10-04T08:20:53.022Z","message":{"role":"user","content":"続けて"},"version":"2.1.289"}`
)

// panesNow は pane 一覧のテストの現在時刻。limitedEntry の解除の 3 時間半前、weeklyEntry の解除の 3 日近く前。
var panesNow = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

// pane 一覧は Claude の pane ごとに block の高さと screen を出す。取れなければ理由を出して pane の行は残す。
func TestReportPanes_Geometry(t *testing.T) {
	client := geometryOf(map[string]multiplexer.Geometry{
		"tall": {Rows: 52, Columns: 139, AlternateScreen: true},
	})
	rep := collectPanes(context.Background(), client, []multiplexer.Pane{
		{PaneID: "tall", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
		{PaneID: "gone", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle},
		{PaneID: "shell", CWD: "/w", Agent: ""},
	}, "", panesNow)
	if len(rep.Claude) != 2 || rep.Claude[0].Geometry == nil || rep.Claude[0].Geometry.Rows != 52 {
		t.Fatalf("Claude = %+v", rep.Claude)
	}
	if rep.Claude[1].Geometry != nil || rep.Claude[1].GeometryError == "" {
		t.Errorf("取れない pane = %+v, want geometry 無しで理由あり", rep.Claude[1])
	}
	if strings.Contains(strings.Join(client.Calls, " "), "PaneGeometry:shell") {
		t.Errorf("Claude でない pane に size / modes を打っている: %v", client.Calls)
	}

	var buf bytes.Buffer
	writePanes(&buf, *rep)
	out := buf.String()
	for _, want := range []string{"rows=52", "screen=alternate", "block call"} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が無い:\n%s", want, out)
		}
	}
}

// pane 一覧は daemon と同じ順で session を引き (statusline の記録、無ければ前景の pid で登録簿)、その transcript の
// 末尾と分類、daemon の扱い (monitor.Decide) を出す。引けない pane には ⚠ を出す (daemon はその pane の上限を判定できない)。
func TestReportPanes_SessionAndTranscript(t *testing.T) {
	claudeDir := writeClaudeConfig(t,
		map[int]string{4242: "sid-reg"},
		map[string]string{"sid-ctx": idleEntry, "sid-reg": limitedEntry, "sid-weekly": weeklyEntry},
	)
	rep := collectPanes(context.Background(), geometryOf(nil), []multiplexer.Pane{
		{PaneID: "b1", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle, SessionID: "sid-ctx", PID: 1111},
		{PaneID: "b2", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusError, PID: 4242},
		{PaneID: "b3", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle, PID: 9999},
		{PaneID: "b4", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle, SessionID: "sid-none"},
		{PaneID: "b5", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusError, SessionID: "sid-weekly"},
	}, claudeDir, panesNow)

	byPane := map[string]PaneReport{}
	for _, p := range rep.Claude {
		byPane[p.PaneID] = p
	}
	if p := byPane["b1"]; p.SessionSource != SessionFromStatusline || p.Transcript.LastEntry == nil || p.Transcript.LastEntry.Kind != "none" || p.Transcript.LastEntry.Plan != "none" {
		t.Errorf("b1 = %+v, want statusline の session と none", p)
	}
	if p := byPane["b2"]; p.SessionID != "sid-reg" || p.SessionSource != SessionFromRegistry || p.Transcript.LastEntry == nil || p.Transcript.LastEntry.Kind != "limit" || p.Transcript.LastEntry.Plan != "wait" {
		t.Errorf("b2 = %+v, want 登録簿の session と limit / wait", p)
	}
	if p := byPane["b5"]; p.Transcript.LastEntry == nil || p.Transcript.LastEntry.Kind != "weekly-limit" || p.Transcript.LastEntry.Plan != "weekly-far" ||
		p.Transcript.LastEntry.ResetsAt == nil || !p.Transcript.LastEntry.ResetsAt.Equal(time.Unix(1791345600, 0)) {
		t.Errorf("b5 = %+v, want weekly-limit / weekly-far / 解除時刻", p)
	}
	if p := byPane["b3"]; p.SessionID != "" || p.Transcript.LastEntry != nil || !strings.Contains(p.Transcript.Reason, "session_id") {
		t.Errorf("b3 = %+v, want session 無しの理由", p)
	}
	if p := byPane["b4"]; p.Transcript.LastEntry != nil || !strings.Contains(p.Transcript.Reason, "見つからない") {
		t.Errorf("b4 = %+v, want transcript が無い理由", p)
	}

	var buf bytes.Buffer
	writePanes(&buf, *rep)
	out := buf.String()
	// 解除時刻は entry の epoch 秒から time.Unix で組み立てるので、表示はローカルの tz になる。
	for _, want := range []string{
		"session_id=sid-ctx (statusline)", "session_id=sid-reg (registry)", "session_id=(なし)",
		"末尾の entry: type=assistant error=rate_limit at 2026-10-04T08:32:13Z → limit (解除 " + time.Unix(1791117000, 0).Format(time.RFC3339) + ")\n      daemon の扱い: 待機に入る",
		"末尾の entry: type=assistant error=rate_limit at 2026-10-04T08:40:00Z → weekly-limit (解除 " + time.Unix(1791345600, 0).Format(time.RFC3339) + ")\n      daemon の扱い: 解除が 24 時間より先なので待たない",
		"末尾の entry: type=user at 2026-10-04T08:20:53Z → none\n",
		"⚠ transcript を読めないため上限を判定できない",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が無い:\n%s", want, out)
		}
	}
	if strings.Count(out, "⚠") != 2 {
		t.Errorf("⚠ は判定できない 2 pane の分だけのはず:\n%s", out)
	}
	if strings.Count(out, "daemon の扱い") != 2 {
		t.Errorf("daemon の扱いは上限の 2 pane の分だけのはず (none の pane には出さない):\n%s", out)
	}
}

// 設定ディレクトリが決まらなければ、全 pane が transcript を読めない理由付きになる (登録簿も引かない)。
func TestReportPanes_WithoutClaudeConfigDir(t *testing.T) {
	rep := collectPanes(context.Background(), geometryOf(nil), []multiplexer.Pane{
		{PaneID: "b1", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle, SessionID: "sid", PID: 1},
	}, "", panesNow)
	if p := rep.Claude[0]; p.SessionSource != SessionFromStatusline || p.Transcript.LastEntry != nil || p.Transcript.Reason == "" {
		t.Errorf("pane = %+v", p)
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
	}, "", panesNow))
	out := buf.String()
	if !strings.Contains(out, reason) {
		t.Errorf("理由が出ていない:\n%s", out)
	}
	if strings.Count(out, "unknown の理由") != 1 {
		t.Errorf("理由の無い pane にも理由の欄が出ている:\n%s", out)
	}
}

// record の kind と msg はあるときだけ pane の行に添える。
func TestReportPanes_RecordKindAndMessage(t *testing.T) {
	var buf bytes.Buffer
	writePanes(&buf, *collectPanes(context.Background(), geometryOf(nil), []multiplexer.Pane{
		{PaneID: "b1", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusBlocked, AgentKind: "permission", AgentMessage: "approve Bash: rm"},
		{PaneID: "b2", CWD: "/w", Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking},
	}, "", panesNow))
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
