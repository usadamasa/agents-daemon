package desktopcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/claudesessions"
	"github.com/usadamasa/agents-daemon/internal/spawn"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

const sid = "8cb3bbba-74de-4582-b9e3-9c94f6fe2843"

// host は spawn.Host のテスト用実装。NewSession に渡った引数を残し、pane は常に claude が前景。
type host struct{ runs [][]string }

func (h *host) NewSession(_ context.Context, _, cwd string, argv []string) (multiplexer.Placement, error) {
	h.runs = append(h.runs, append([]string{cwd}, argv...))
	return multiplexer.Placement{SessionID: "session:1", WindowID: "window:1", PaneID: "block:1"}, nil
}

func (h *host) NewWindow(context.Context, string, string, string, []string) (multiplexer.Placement, error) {
	panic("resume はタブを足さない")
}

func (h *host) PaneGet(_ context.Context, id string) (multiplexer.Pane, error) {
	return multiplexer.Pane{PaneID: id, Agent: "claude"}, nil
}

// fixture は登録簿と transcript を置いた resumer を返す。registry は sessions/ に置くファイルの中身 (pid.json → body)、
// alive は生きている pid を返す関数。transcript は repo を cwd にしたものを置く。
func fixture(t *testing.T, registry map[string]string, alive func(int) bool) (resumer, *host, string) {
	t.Helper()
	config := t.TempDir()
	repo := t.TempDir()
	sessions := filepath.Join(config, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range registry {
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(strings.ReplaceAll(body, "REPO", repo)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(config, "projects", transcript.ProjectDirName(repo))
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, sid+".jsonl"), []byte(`{"type":"user","cwd":"`+repo+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := claudesessions.New(config)
	store.Alive = alive
	h := &host{}
	return resumer{
		sessions:    store,
		transcripts: transcript.New(config),
		runner:      spawn.Runner{Host: h, WaitTimeout: 50 * time.Millisecond, WaitInterval: time.Millisecond},
		interval:    time.Millisecond,
	}, h, repo
}

const desktopEntry = `{"pid":93176,"sessionId":"` + sid + `","cwd":"REPO","entrypoint":"claude-desktop","status":"idle"}`

// Desktop で閉じたセッション (登録簿に生きた process が無い) は transcript の cwd で rex に開き直す。
func TestResume_closed(t *testing.T) {
	r, h, repo := fixture(t, map[string]string{"93176.json": desktopEntry}, func(int) bool { return false })
	got, err := r.run(context.Background(), &bytes.Buffer{}, sid, "", 0)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if want := (spawn.Result{SessionID: sid, PaneID: "block:1", RexSessionID: "session:1", WindowID: "window:1", Foreground: true}); got != want {
		t.Errorf("run() = %+v, want %+v", got, want)
	}
	if want := [][]string{{repo, "claude", "--resume", sid}}; !reflect.DeepEqual(h.runs, want) {
		t.Errorf("NewSession = %v, want %v", h.runs, want)
	}
}

// Desktop で開いたままなら、--wait が無ければ開かずに error。
func TestResume_openWithoutWait(t *testing.T) {
	r, h, _ := fixture(t, map[string]string{"93176.json": desktopEntry}, func(int) bool { return true })
	_, err := r.run(context.Background(), &bytes.Buffer{}, sid, "", 0)
	if err == nil || !strings.Contains(err.Error(), "Desktop で開いている") {
		t.Fatalf("run() error = %v, want Desktop で開いている", err)
	}
	if len(h.runs) != 0 {
		t.Errorf("開いたままのセッションを rex で開いた: %v", h.runs)
	}
}

// --wait なら閉じるのを待ってから、登録簿の cwd で開く。待っている間は stderr に閉じる案内を出す。
func TestResume_waitsForClose(t *testing.T) {
	checks := 0
	r, h, repo := fixture(t, map[string]string{"93176.json": desktopEntry}, func(int) bool { checks++; return checks < 4 })
	var stderr bytes.Buffer
	if _, err := r.run(context.Background(), &stderr, sid, "", time.Second); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Desktop でセッション "+sid+" を閉じて") {
		t.Errorf("stderr = %q, want 閉じる案内", stderr.String())
	}
	if want := [][]string{{repo, "claude", "--resume", sid}}; !reflect.DeepEqual(h.runs, want) {
		t.Errorf("NewSession = %v, want %v", h.runs, want)
	}
}

// --wait の間に閉じなければ開かずに error。
func TestResume_waitTimeout(t *testing.T) {
	r, h, _ := fixture(t, map[string]string{"93176.json": desktopEntry}, func(int) bool { return true })
	_, err := r.run(context.Background(), &bytes.Buffer{}, sid, "", 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "閉じなかった") {
		t.Fatalf("run() error = %v, want 閉じなかった", err)
	}
	if len(h.runs) != 0 {
		t.Errorf("閉じなかったセッションを rex で開いた: %v", h.runs)
	}
}

// 端末 (cli) で開いているセッションは --wait でも待たずに error (rex かほかの端末で既に続いている)。
func TestResume_openInCLI(t *testing.T) {
	cli := `{"pid":200,"sessionId":"` + sid + `","cwd":"REPO","entrypoint":"cli"}`
	r, h, _ := fixture(t, map[string]string{"200.json": cli}, func(int) bool { return true })
	_, err := r.run(context.Background(), &bytes.Buffer{}, sid, "", time.Second)
	if err == nil || !strings.Contains(err.Error(), "Desktop ではなく cli") {
		t.Fatalf("run() error = %v, want Desktop ではなく cli", err)
	}
	if len(h.runs) != 0 {
		t.Errorf("開いているセッションを rex で開いた: %v", h.runs)
	}
}

// --cwd は登録簿と transcript より先に使う。transcript から cwd が分からなければ error。
func TestResume_cwd(t *testing.T) {
	r, h, _ := fixture(t, nil, func(int) bool { return false })
	other := t.TempDir()
	if _, err := r.run(context.Background(), &bytes.Buffer{}, sid, other, 0); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if h.runs[0][0] != other {
		t.Errorf("cwd = %s, want --cwd の %s", h.runs[0][0], other)
	}
	if _, err := r.run(context.Background(), &bytes.Buffer{}, "unknown-session", "", 0); err == nil || !strings.Contains(err.Error(), "--cwd") {
		t.Errorf("run() error = %v, want --cwd の案内", err)
	}
}

func TestWriteList(t *testing.T) {
	var b bytes.Buffer
	if err := writeList(&b, nil); err != nil || !strings.Contains(b.String(), "無い") {
		t.Errorf("writeList(nil) = %q, %v", b.String(), err)
	}
	b.Reset()
	err := writeList(&b, []claudesessions.Session{{PID: 1, SessionID: sid, Name: "desk", Cwd: "/src/a", Status: "idle"}})
	if err != nil || !strings.Contains(b.String(), sid) || !strings.Contains(b.String(), "/src/a") {
		t.Errorf("writeList = %q, %v", b.String(), err)
	}
}
