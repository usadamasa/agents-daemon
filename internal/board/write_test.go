package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/domain"
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

func writeBoard(t *testing.T, p apppath.Paths, v Viewer) Board {
	t.Helper()
	var buf bytes.Buffer
	v.UserHome = "/u"
	if err := Write(&buf, p, v); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var b Board
	if err := json.Unmarshal(buf.Bytes(), &b); err != nil {
		t.Fatalf("JSON = %s: %v", buf.String(), err)
	}
	return b
}

// daemon が動いていなくても、開いたセッションの domain のタスクを描く。domain は config と cwd で決め、
// 開いたセッションの記録が業務の側にあれば業務を描く。
func TestWrite(t *testing.T) {
	p := paths(t)
	writeFile(t, p.ConfigFile(), `{"homePaths": ["/src/home"]}`)
	tasks := filepath.Join(p.StateDir(), "tasks")
	writeFile(t, filepath.Join(tasks, "home", "H", "1.json"), `{"id":"1","subject":"私用","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	writeFile(t, filepath.Join(tasks, "work", "W", "1.json"), `{"id":"1","subject":"業務","status":"pending","session_id":"W","cwd":"/work/b"}`)

	home := writeBoard(t, p, Viewer{CWD: "/src/home/a"})
	if home.Domain != domain.Home || home.DaemonRunning || len(home.Rows) != 1 || home.Rows[0].Cards[0].Subject != "私用" {
		t.Errorf("home = %+v", home)
	}
	work := writeBoard(t, p, Viewer{CWD: "/work/b"})
	if work.Domain != domain.Work || len(work.Rows) != 1 || work.Rows[0].Cards[0].Subject != "業務" {
		t.Errorf("work = %+v", work)
	}
	// 私用の cwd でも、記録が業務の側にあるセッションが開いたら業務を描く。
	moved := writeBoard(t, p, Viewer{SessionID: "W", CWD: "/src/home/a"})
	if moved.Domain != domain.Work {
		t.Errorf("moved.Domain = %q, want work", moved.Domain)
	}
}

// パーキングは開いたセッションの domain の open のメモだけを載せる。
func TestWrite_parking(t *testing.T) {
	p := paths(t)
	writeFile(t, p.ConfigFile(), `{"homePaths": ["/src/home"]}`)
	dir := filepath.Join(p.StateDir(), "parking")
	src := `"sources":[{"session_id":"H","cwd":"/src/home/a","at":"2026-10-18T09:00:00Z"}]`
	writeFile(t, filepath.Join(dir, "home", "1.json"), `{"id":"1","repo":"/src/home/a","kind":"aside","title":"開いている","status":"open",`+src+`}`)
	writeFile(t, filepath.Join(dir, "home", "2.json"), `{"id":"2","repo":"/src/home/a","kind":"aside","title":"捨てた","status":"discarded",`+src+`}`)
	writeFile(t, filepath.Join(dir, "work", "3.json"), `{"id":"3","repo":"/work/b","kind":"aside","title":"業務","status":"open",`+src+`}`)

	home := writeBoard(t, p, Viewer{CWD: "/src/home/a"})
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
	writeFile(t, p.ConfigFile(), `{"homePaths": ["/src/home"]}`)
	const id = "20261025T010000-0000000a"
	writeFile(t, filepath.Join(p.StateDir(), "tasks", "home", "H", "1.json"), `{"id":"1","subject":"渡されたメモ","description":"(メモ `+id+`)","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	writeFile(t, filepath.Join(p.StateDir(), "parking", "home", id+".json"), `{"id":"`+id+`","repo":"/src/home/a","kind":"aside","title":"渡した","status":"handed","sources":[{"session_id":"H","cwd":"/src/home/a","at":"2026-10-18T09:00:00Z"}]}`)

	home := writeBoard(t, p, Viewer{CWD: "/src/home/a"})
	if len(home.Graph.Nodes) != 1 || len(home.Graph.Nodes[0].Memos) != 1 || home.Graph.Nodes[0].Memos[0].Title != "渡した" {
		t.Errorf("nodes = %+v", home.Graph.Nodes)
	}
	if len(home.Rows) != 1 || len(home.Rows[0].Parking) != 0 {
		t.Errorf("handed のメモはパーキングの列に出ない: %+v", home.Rows)
	}
}

// グラフの辺には、開いたセッションの domain の辺 (edges/<domain>/) だけを使う。
func TestWrite_edges(t *testing.T) {
	p := paths(t)
	writeFile(t, p.ConfigFile(), `{"homePaths": ["/src/home"]}`)
	tasks := filepath.Join(p.StateDir(), "tasks")
	writeFile(t, filepath.Join(tasks, "home", "H", "1.json"), `{"id":"1","subject":"前","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	writeFile(t, filepath.Join(tasks, "home", "H", "2.json"), `{"id":"2","subject":"後","status":"pending","session_id":"H","cwd":"/src/home/a"}`)
	ep := func(ref, cwd string) string { return `{"ref":"` + ref + `","cwd":"` + cwd + `","repo":"` + cwd + `"}` }
	dir := filepath.Join(p.StateDir(), "edges")
	writeFile(t, filepath.Join(dir, "home", "e1.json"), `{"id":"e1","domain":"home","before":`+ep("task:H/1", "/src/home/a")+`,"after":`+ep("task:H/2", "/src/home/a")+`,"created_at":"2026-10-18T09:00:00Z"}`)
	writeFile(t, filepath.Join(dir, "work", "e2.json"), `{"id":"e2","domain":"work","before":`+ep("issue:o/r#1", "/work/b")+`,"after":`+ep("task:H/1", "/work/b")+`,"created_at":"2026-10-18T09:00:00Z"}`)

	home := writeBoard(t, p, Viewer{CWD: "/src/home/a"})
	if len(home.Graph.Links) != 1 || home.Graph.Links[0].Before != "H/1" || home.Graph.Links[0].After != "H/2" || len(home.Graph.Nodes) != 2 {
		t.Errorf("graph = %+v", home.Graph)
	}
}
