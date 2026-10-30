package verify

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
)

// record の kind と msg は、あるときだけ pane の行に添える (上限で止まった pane の自己申告を status から読むため)。
func TestPrintSnapshot_record(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	snap := &daemonstate.StatusSnapshot{PID: 42, Backend: "rex", StartedAt: now, UpdatedAt: now, Panes: []daemonstate.PaneStatus{
		{PaneID: "p1", CWD: "/w", AgentStatus: "blocked", AgentKind: "permission", AgentMessage: "approve Bash: rm -rf ./tmp/junk", MonitorStatus: "waiting", LastTickAt: now},
		{PaneID: "p2", CWD: "/w", AgentStatus: "working", MonitorStatus: "monitoring", LastTickAt: now},
	}}
	var buf strings.Builder
	printSnapshot(&buf, snap, now)
	out := buf.String()
	for _, want := range []string{`kind=permission`, `msg="approve Bash: rm -rf ./tmp/junk"`} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が無い:\n%s", want, out)
		}
	}
	if strings.Count(out, "kind=") != 1 {
		t.Errorf("kind の無い pane にも kind= が出ている:\n%s", out)
	}
}

// CollectStatus は `status --json` がそのまま出す。前の daemon が残した snapshot (pid が違う) は見せない。
func TestCollectStatus(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	t.Run("起動していなければ running が false", func(t *testing.T) {
		rep, err := CollectStatus(apppath.New(t.TempDir(), "", ""))
		if err != nil {
			t.Fatalf("CollectStatus() error = %v", err)
		}
		if rep.Running || rep.Snapshot != nil || rep.LogTail == nil {
			t.Errorf("rep = %+v, want running=false で log_tail は空の配列", rep)
		}
	})
	t.Run("起動中なら pid と今の daemon の snapshot とログの末尾", func(t *testing.T) {
		p := apppath.New(t.TempDir(), "", "")
		writeFixturePIDFile(t, p.PIDFile(), os.Getpid())
		writeFixtureSnapshot(t, p.StatusFile(), daemonstate.StatusSnapshot{PID: os.Getpid() + 1, StartedAt: now, UpdatedAt: now})
		writeFile(t, p.LogFile(), "a\nb\n")
		rep, err := CollectStatus(p)
		if err != nil {
			t.Fatalf("CollectStatus() error = %v", err)
		}
		if !rep.Running || rep.PID != os.Getpid() || rep.Snapshot != nil || len(rep.LogTail) != 2 {
			t.Errorf("rep = %+v, want 起動中・前の daemon の snapshot は nil・ログ 2 行", rep)
		}
		body, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if !strings.Contains(string(body), `"snapshot":null`) || !strings.Contains(string(body), `"log_tail":["a","b"]`) {
			t.Errorf("JSON = %s", body)
		}
	})
}

func TestPrintSnapshot_backend(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		backend string
		want    string
	}{
		{"記録があれば表示する", "rex", "backend: rex"},
		{"変更前の daemon が書いた snapshot は記録なし", "", "backend: (記録なし)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			printSnapshot(&buf, &daemonstate.StatusSnapshot{PID: 42, Backend: tc.backend, StartedAt: now, UpdatedAt: now}, now)
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("出力に %q が無い:\n%s", tc.want, buf.String())
			}
		})
	}
}
