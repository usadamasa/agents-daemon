package claudesessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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

// registry は Desktop 由来と端末由来の混ざった登録簿を置き、alive の pid だけを生きているとする Store を返す。
func registry(t *testing.T, alive ...int) Store {
	t.Helper()
	config := t.TempDir()
	dir := filepath.Join(config, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "100.json", `{"pid":100,"sessionId":"D2","name":"desk-b","cwd":"/src/b","entrypoint":"claude-desktop","status":"busy","hostSessionId":"local_b"}`)
	write(t, dir, "200.json", `{"pid":200,"sessionId":"D1","cwd":"/src/a","entrypoint":"claude-desktop","status":"idle","hostSessionId":"local_a"}`)
	write(t, dir, "300.json", `{"pid":300,"sessionId":"C1","cwd":"/src/c","entrypoint":"cli","status":"idle"}`)
	write(t, dir, "400.json", `{"pid":400,"sessionId":"D3","cwd":"/src/d","entrypoint":"claude-desktop"}`)
	write(t, dir, "500.json", `{"pid":500,"cwd":"/src/e","entrypoint":"claude-desktop"}`)
	s := New(config)
	s.Alive = func(pid int) bool {
		for _, a := range alive {
			if a == pid {
				return true
			}
		}
		return false
	}
	return s
}

// Desktop は entrypoint=claude-desktop で process が生きているものだけを session ID の順に返す。端末由来、process の
// 居ない残り物、session ID の無いものは載せない。
func TestDesktop(t *testing.T) {
	got, err := registry(t, 100, 200, 300, 500).Desktop()
	if err != nil {
		t.Fatalf("Desktop() error = %v", err)
	}
	want := []Session{
		{PID: 200, SessionID: "D1", Cwd: "/src/a", Entrypoint: EntrypointDesktop, Status: "idle"},
		{PID: 100, SessionID: "D2", Name: "desk-b", Cwd: "/src/b", Entrypoint: EntrypointDesktop, Status: "busy"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desktop() = %+v, want %+v", got, want)
	}
}

// Find は session ID で生きているセッションを引く。process が居なければ (閉じた) ok=false。
func TestFind(t *testing.T) {
	s := registry(t, 100)
	if got, ok, err := s.Find("D2"); err != nil || !ok || got.PID != 100 || got.Cwd != "/src/b" {
		t.Errorf("Find(D2) = %+v, %v, %v, want pid 100 の /src/b", got, ok, err)
	}
	for _, id := range []string{"D3", "missing"} {
		if _, ok, err := s.Find(id); err != nil || ok {
			t.Errorf("Find(%s) = %v, %v, want 見つからない", id, ok, err)
		}
	}
}

// WaitClosed は process が居なくなるまで待ち、ctx が切れたら ctx の error を返す。
func TestWaitClosed(t *testing.T) {
	s := registry(t)
	calls := 0
	s.Alive = func(pid int) bool { calls++; return calls < 3 }
	if err := s.WaitClosed(context.Background(), "D2", time.Millisecond); err != nil {
		t.Fatalf("WaitClosed() error = %v", err)
	}
	if calls < 3 {
		t.Errorf("閉じる前に戻った (calls=%d)", calls)
	}
	s.Alive = func(int) bool { return true }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.WaitClosed(ctx, "D2", time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitClosed() error = %v, want DeadlineExceeded", err)
	}
}

// processAlive は自分の process を居ると読み、0 以下の pid は居ないと読む。
func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("自分の pid を居ないと読んだ")
	}
	for _, pid := range []int{0, -1} {
		if processAlive(pid) {
			t.Errorf("processAlive(%d) = true", pid)
		}
	}
}
