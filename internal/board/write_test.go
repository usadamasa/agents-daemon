package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

func paths(t *testing.T) apppath.Paths {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	p, err := apppath.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBoard(t *testing.T, p apppath.Paths, cwd string) Board {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, p, cwd); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var b Board
	if err := json.Unmarshal(buf.Bytes(), &b); err != nil {
		t.Fatalf("JSON = %s: %v", buf.String(), err)
	}
	return b
}

// daemon が動いていなくても、タスクの記録を描く。
func TestWrite(t *testing.T) {
	p := paths(t)
	tasks := filepath.Join(p.StateDir(), "tasks")
	writeFile(t, filepath.Join(tasks, "H", "1.json"), `{"id":"1","subject":"私用","status":"pending","session_id":"H","cwd":"/src/home/a"}`)

	b := writeBoard(t, p, "/src/home/a")
	if b.DaemonRunning || len(b.Rows) != 1 || b.Rows[0].Cards[0].Subject != "私用" {
		t.Errorf("board = %+v", b)
	}
}

// パーキングには open のメモだけを載せる。
func TestWrite_parking(t *testing.T) {
	p := paths(t)
	dir := filepath.Join(p.StateDir(), "parking")
	src := `"sources":[{"session_id":"H","cwd":"/src/home/a","at":"2026-10-18T09:00:00Z"}]`
	writeFile(t, filepath.Join(dir, "1.json"), `{"id":"1","repo":"/src/home/a","kind":"aside","title":"開いている","status":"open",`+src+`}`)
	writeFile(t, filepath.Join(dir, "2.json"), `{"id":"2","repo":"/src/home/a","kind":"aside","title":"捨てた","status":"discarded",`+src+`}`)

	home := writeBoard(t, p, "/src/home/a")
	if len(home.Rows) != 1 || len(home.Rows[0].Parking) != 1 || home.Rows[0].Parking[0].Title != "開いている" {
		t.Errorf("home = %+v", home.Rows)
	}
	// here は開いたセッションの行の鍵 (mod が自分の repo のメモを引く)。行の鍵と同じ規則で決める。
	if home.Here != home.Rows[0].Repo {
		t.Errorf("here = %q, want %q", home.Here, home.Rows[0].Repo)
	}
}

// タスクの description との照合には open 以外のメモも読む。
func TestWrite_memos(t *testing.T) {
	p := paths(t)
	const id = "20261025T010000-0000000a"
	writeFile(t, filepath.Join(p.StateDir(), "tasks", "H", "1.json"), `{"id":"1","subject":"渡されたメモ","description":"(メモ `+id+`)","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	writeFile(t, filepath.Join(p.StateDir(), "parking", id+".json"), `{"id":"`+id+`","repo":"/src/home/a","kind":"aside","title":"渡した","status":"handed","sources":[{"session_id":"H","cwd":"/src/home/a","at":"2026-10-18T09:00:00Z"}]}`)

	home := writeBoard(t, p, "/src/home/a")
	if len(home.Graph.Nodes) != 1 || len(home.Graph.Nodes[0].Memos) != 1 || home.Graph.Nodes[0].Memos[0].Title != "渡した" {
		t.Errorf("nodes = %+v", home.Graph.Nodes)
	}
	if len(home.Rows) != 1 || len(home.Rows[0].Parking) != 0 {
		t.Errorf("handed のメモはパーキングの列に出ない: %+v", home.Rows)
	}
}

// グラフの辺は edges/ のファイルから読む。
func TestWrite_edges(t *testing.T) {
	p := paths(t)
	tasks := filepath.Join(p.StateDir(), "tasks")
	writeFile(t, filepath.Join(tasks, "H", "1.json"), `{"id":"1","subject":"前","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	writeFile(t, filepath.Join(tasks, "H", "2.json"), `{"id":"2","subject":"後","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	ep := func(ref, cwd string) string { return `{"ref":"` + ref + `","cwd":"` + cwd + `","repo":"` + cwd + `"}` }
	dir := filepath.Join(p.StateDir(), "edges")
	writeFile(t, filepath.Join(dir, "e1.json"), `{"id":"e1","before":`+ep("task:H/1", "/src/home/a")+`,"after":`+ep("task:H/2", "/src/home/a")+`,"created_at":"2026-10-18T09:00:00Z"}`)

	home := writeBoard(t, p, "/src/home/a")
	if len(home.Graph.Links) != 1 || home.Graph.Links[0].Before != "H/1" || home.Graph.Links[0].After != "H/2" || len(home.Graph.Nodes) != 2 {
		t.Errorf("graph = %+v", home.Graph)
	}
}
