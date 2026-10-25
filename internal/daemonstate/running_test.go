package daemonstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeSnapshot は pid の daemon が at に書いた status.json を置く。
func writeSnapshot(t *testing.T, p apppath.Paths, pid int, at time.Time) {
	t.Helper()
	data, err := json.Marshal(StatusSnapshot{PID: pid, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, p.StatusFile(), string(data))
}

// Running は動いている daemon (このテストプロセス) の pid を返し、前の daemon が残した status.json は今の daemon の
// ものとして返さない。
func TestRunning(t *testing.T) {
	me := os.Getpid()
	root := t.TempDir()
	p := apppath.New(root, filepath.Join(root, "state"), "")
	if pid, snap, err := Running(p); err != nil || pid != 0 || snap != nil {
		t.Errorf("pid ファイルが無い: Running() = %d, %v, %v", pid, snap, err)
	}
	writeTestFile(t, p.PIDFile(), strconv.Itoa(me)+"\n")
	if pid, snap, err := Running(p); err != nil || pid != me || snap != nil {
		t.Errorf("status.json が無い: Running() = %d, %v, %v", pid, snap, err)
	}
	writeSnapshot(t, p, me+100000, time.Now())
	if pid, snap, err := Running(p); err != nil || pid != me || snap != nil {
		t.Errorf("前の daemon の status.json: Running() = %d, %v, %v", pid, snap, err)
	}
	writeSnapshot(t, p, me, time.Now())
	if pid, snap, err := Running(p); err != nil || pid != me || snap == nil || snap.PID != me {
		t.Errorf("今の daemon の status.json: Running() = %d, %v, %v", pid, snap, err)
	}
}
