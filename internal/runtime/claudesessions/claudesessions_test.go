package claudesessions

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Names は <config>/sessions/<pid>.json から session ID → ListAgents の名前を引く。名前か session ID の無いもの、
// 壊れたもの、.json でないものは読み飛ばす。
func TestNames(t *testing.T) {
	config := t.TempDir()
	dir := filepath.Join(config, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "100.json", `{"pid":100,"sessionId":"S1","name":"agents-daemon-4b","nameSource":"derived"}`)
	write(t, dir, "200.json", `{"pid":200,"sessionId":"S2","name":"peer-b","nameSource":"user"}`)
	write(t, dir, "300.json", `{"pid":300,"sessionId":"S3"}`)
	write(t, dir, "400.json", `{"pid":400,"name":"no-id"}`)
	write(t, dir, "500.json", `{broken`)
	write(t, dir, "600.txt", `{"sessionId":"S6","name":"txt"}`)

	got, err := New(config).Names()
	if err != nil {
		t.Fatalf("Names() error = %v", err)
	}
	want := map[string]string{"S1": "agents-daemon-4b", "S2": "peer-b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// SessionIDs は pid → session ID を引く。rex の foreground の pid から、statusline の記録が無い pane の session を
// 補うのに使う。名前の無いセッションも載せ、pid か session ID の無いもの、壊れたものは読み飛ばす。
func TestSessionIDs(t *testing.T) {
	config := t.TempDir()
	dir := filepath.Join(config, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "100.json", `{"pid":100,"sessionId":"S1","name":"agents-daemon-4b"}`)
	write(t, dir, "300.json", `{"pid":300,"sessionId":"S3"}`)
	write(t, dir, "400.json", `{"pid":400,"name":"no-id"}`)
	write(t, dir, "500.json", `{"sessionId":"S5"}`)
	write(t, dir, "600.json", `{broken`)

	got, err := New(config).SessionIDs()
	if err != nil {
		t.Fatalf("SessionIDs() error = %v", err)
	}
	want := map[int]string{100: "S1", 300: "S3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SessionIDs() = %v, want %v", got, want)
	}
}

// Fill は session の無い Claude の pane だけを前景の pid で補う。statusline の記録のある pane と Claude でない pane は触らない。
func TestFill(t *testing.T) {
	config := t.TempDir()
	dir := filepath.Join(config, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "100.json", `{"pid":100,"sessionId":"S1"}`)
	panes := []multiplexer.Pane{
		{PaneID: "a", Agent: "claude", PID: 100},
		{PaneID: "b", Agent: "claude", PID: 100, SessionID: "from-statusline"},
		{PaneID: "c", Agent: "claude", PID: 999},
		{PaneID: "d", Agent: "", PID: 100},
	}
	if err := New(config).Fill(panes); err != nil {
		t.Fatalf("Fill() error = %v", err)
	}
	want := []string{"S1", "from-statusline", "", ""}
	for i, p := range panes {
		if p.SessionID != want[i] {
			t.Errorf("panes[%d] (%s).SessionID = %q, want %q", i, p.PaneID, p.SessionID, want[i])
		}
	}
	// 補う pane が無ければ登録簿を読まない (無い Store でも error にならない)。
	if err := (Store{Dir: filepath.Join(config, "none")}).Fill([]multiplexer.Pane{{Agent: "claude", SessionID: "x", PID: 1}}); err != nil {
		t.Errorf("Fill() error = %v", err)
	}
}

// 登録簿が無い・ゼロ値の Store は空を返す。
func TestNames_missing(t *testing.T) {
	for _, s := range []Store{New(t.TempDir()), {}} {
		got, err := s.Names()
		if err != nil || len(got) != 0 {
			t.Errorf("Names() = %v, %v, want 空", got, err)
		}
		ids, err := s.SessionIDs()
		if err != nil || len(ids) != 0 {
			t.Errorf("SessionIDs() = %v, %v, want 空", ids, err)
		}
	}
}
