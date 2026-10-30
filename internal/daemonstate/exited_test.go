package daemonstate

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// writeRunning は動いている daemon (このテストプロセス) の pid と、その daemon が at に書いた status.json を置く。
func writeRunning(t *testing.T, p apppath.Paths, pid int, at time.Time, panes ...PaneStatus) {
	t.Helper()
	writeTestFile(t, p.PIDFile(), strconv.Itoa(pid)+"\n")
	data, err := json.Marshal(StatusSnapshot{PID: pid, UpdatedAt: at, Panes: panes})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, p.StatusFile(), string(data))
}

// SessionExited は「今の daemon が after より後に書いた status.json に、そのセッションの pane が無い」ときだけ nil。
// それ以外は理由を返し、adopt (parking.Adopt) はそれを拒む理由にする (models/handoff の AdoptWrite)。
func TestSessionExited(t *testing.T) {
	me := os.Getpid()
	recorded := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		setup   func(t *testing.T, p apppath.Paths)
		wantErr string
	}{
		{name: "daemon が動いていない", setup: func(*testing.T, apppath.Paths) {}, wantErr: "daemon が動いていない"},
		{
			name: "status.json が前の daemon のもの",
			setup: func(t *testing.T, p apppath.Paths) {
				writeRunning(t, p, me, recorded.Add(time.Minute))
				writeSnapshot(t, p, me+100000, recorded.Add(time.Minute))
			},
			wantErr: "まだ status.json を書いていない",
		},
		{
			name:    "status.json がタスクの記録より古い",
			setup:   func(t *testing.T, p apppath.Paths) { writeRunning(t, p, me, recorded.Add(-time.Second)) },
			wantErr: "記録より古い",
		},
		{
			name: "pane がある (生きている)",
			setup: func(t *testing.T, p apppath.Paths) {
				writeRunning(t, p, me, recorded.Add(time.Minute), PaneStatus{PaneID: "b1", SessionID: "dead", CWD: "/work/x"})
			},
			wantErr: "生きている",
		},
		{
			name: "記録より後の status.json に pane が無い",
			setup: func(t *testing.T, p apppath.Paths) {
				writeRunning(t, p, me, recorded.Add(time.Minute), PaneStatus{PaneID: "b2", SessionID: "other"})
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := apppath.New(t.TempDir(), "", "")
			c.setup(t, p)
			err := SessionExited(p, "dead", recorded)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("SessionExited() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("SessionExited() error = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}
