package daemoncli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon"
	"github.com/usadamasa/agents-daemon/internal/runtime/verify"
)

// isolate は HOME と XDG を一時ディレクトリへ向け、REX_SESSION を外す。返り値は state の agents-daemon ディレクトリ。
func isolate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("REX_SESSION", "")
	return filepath.Join(root, "state", "agents-daemon")
}

// runJSON は --json を付けて args を root で動かし、stdout を返す。
func runJSON(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := testRoot()
	jsonflag.Add(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"--json"}, args...))
	err := root.Execute()
	return out.String(), err
}

func decode(t *testing.T, out string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("JSON = %q: %v", out, err)
	}
}

// logs --json はログのパスと末尾の行を出す。ログが無ければ lines は空の配列。
func TestLogs_json(t *testing.T) {
	state := isolate(t)
	out, err := runJSON(t, "logs", "-n", "1")
	if err != nil {
		t.Fatal(err)
	}
	var empty verify.LogsReport
	decode(t, out, &empty)
	if empty.Path != filepath.Join(state, "logs", "daemon.log") || empty.Lines == nil || len(empty.Lines) != 0 {
		t.Errorf("ログ無し = %+v, want daemon.log のパスと空の lines", empty)
	}

	writeFile(t, filepath.Join(state, "logs", "daemon.log"), "一行目\n二行目\n")
	out, err = runJSON(t, "logs", "-n", "1")
	if err != nil {
		t.Fatal(err)
	}
	var rep verify.LogsReport
	decode(t, out, &rep)
	if len(rep.Lines) != 1 || rep.Lines[0] != "二行目" {
		t.Errorf("lines = %v, want [二行目]", rep.Lines)
	}
}

// stop --json は daemon が起動していなければ was_running=false を出す。
func TestStop_json(t *testing.T) {
	isolate(t)
	out, err := runJSON(t, "stop")
	if err != nil {
		t.Fatal(err)
	}
	var res daemonstate.StopResult
	decode(t, out, &res)
	if res.WasRunning || res.PID != 0 {
		t.Errorf("stop = %+v, want was_running=false", res)
	}
}

// daemon --ensure --json は rex の外では outside_rex を出して exit 0。--foreground は JSON にできないので error。
func TestDaemon_json(t *testing.T) {
	isolate(t)
	out, err := runJSON(t, "daemon", "--ensure")
	if err != nil {
		t.Fatalf("--ensure: %v", err)
	}
	var rep daemon.EnsureReport
	decode(t, out, &rep)
	if rep.Action != daemon.EnsureOutsideRex {
		t.Errorf("action = %q, want %q", rep.Action, daemon.EnsureOutsideRex)
	}

	if _, err := runJSON(t, "daemon", "--foreground"); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Errorf("--foreground: error = %v, want --json に対応していない error", err)
	}
}

// inspect --file --json は判定と走査した行、ゲート、起床時刻の根拠を 1 つの JSON で出す。
func TestInspect_json(t *testing.T) {
	isolate(t)
	file := filepath.Join(t.TempDir(), "screen.txt")
	screen := "⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)\n  /upgrade to increase your usage limit.\n"
	if err := os.WriteFile(file, []byte(screen), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runJSON(t, "inspect", "--file", file)
	if err != nil {
		t.Fatal(err)
	}
	var rep verify.InspectReport
	decode(t, out, &rep)
	if rep.Kind != "limit" || rep.Source != "file:"+file || len(rep.ScannedLines) == 0 || rep.Gate.ID != "no-state" || rep.Wake == nil {
		t.Errorf("inspect = %s", out)
	}
}
