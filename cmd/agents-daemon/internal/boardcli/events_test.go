package boardcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/boardlog"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// boardLines はボードのログの行から時刻を落としたもの。ファイルが無ければ nil。
func boardLines(t *testing.T, stateDir string) []string {
	t.Helper()
	data, err := os.ReadFile(boardlog.Path(stateDir)) // #nosec G304 -- テストの一時ディレクトリ配下
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		_, rest, _ := strings.Cut(line, " ")
		out = append(out, rest)
	}
	return out
}

func equalLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLogIngest(t *testing.T) {
	dir := t.TempDir()
	logIngest(dir, tasklog.Result{Tool: "TaskList", SessionID: "s1", Written: 2, Removed: 1}, nil, time.Now())
	logIngest(dir, tasklog.Result{Tool: "TaskCreate", SessionID: "s1", AgentID: "a1", Written: 1}, nil, time.Now())
	// 記録しないツールは書かない。
	logIngest(dir, tasklog.Result{}, nil, time.Now())
	equalLines(t, boardLines(t, dir), []string{
		"ingest-task session=s1 tool=TaskList written=2 removed=1",
		"ingest-task session=s1 agent=a1 tool=TaskCreate written=1 removed=0",
	})

	logIngest(dir, tasklog.Result{}, errors.New("hook 入力の JSON を読めない"), time.Now())
	logIngest(dir, tasklog.Result{Tool: "TaskCreate", SessionID: "s2"}, errors.New(`task ID "../x" はファイル名に使えない`), time.Now())
	got := boardLines(t, dir)
	if len(got) != 4 || got[2] != `ingest-task.failed reason="hook 入力の JSON を読めない"` ||
		got[3] != `ingest-task.failed session=s2 tool=TaskCreate reason="task ID \"../x\" はファイル名に使えない"` {
		t.Errorf("lines = %q", got)
	}
}

// park の操作はボードのログに書き、メモの title と detail とタスクの subject は書かない。
func TestParkEvents(t *testing.T) {
	dir := t.TempDir()
	s := parking.New(dir)
	p := tasklog.New(dir)
	now := time.Now()

	added, err := parkAdd(dir, s, strings.NewReader(`{"kind": "aside", "title": "秘密の題", "detail": "秘密の詳細", "session_id": "s1"}`), "/src/a", p, now)
	if err != nil || !added.Created {
		t.Fatalf("parkAdd = %+v, %v", added, err)
	}
	if _, err := parkAdd(dir, s, strings.NewReader(`{"kind": "aside", "title": "秘密の題", "session_id": "s2"}`), "/src/a", p, now); err != nil {
		t.Fatal(err)
	}
	if _, err := parkAdd(dir, s, strings.NewReader(`{"kind": `), "/src/a", p, now); err == nil {
		t.Fatal("壊れた JSON が通った")
	}
	if _, err := parkSet(dir, s, "viewer", added.Item.ID, parking.StatusDiscarded, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := parkSet(dir, s, "viewer", "missing", parking.StatusDiscarded, "", now); err == nil {
		t.Fatal("無いメモを書き換えた")
	}
	if _, err := parkSet(dir, s, "viewer", added.Item.ID, parking.StatusHanded, parking.StatusOpen, now); err == nil {
		t.Fatal("捨てたメモを --from open で取れた")
	}
	writeTestConfig(t, filepath.Join(dir, "tasks", "dead", "4.json"), `{"id": "4", "subject": "秘密のタスク", "status": "pending", "session_id": "dead", "cwd": "/src/a"}`)
	exited := func(string, time.Time) error { return nil }
	adopted, err := parkAdopt(dir, s, p, "viewer", "dead", "4", exited, now)
	if err != nil {
		t.Fatal(err)
	}

	id := added.Item.ID
	got := boardLines(t, dir)
	equalLines(t, got, []string{
		"park.add session=s1 memo=" + id + " kind=aside created=true",
		"park.add session=s2 memo=" + id + " kind=aside created=false",
		`park.failed op=add reason="stdin の JSON を読めない: unexpected EOF"`,
		"park.set session=viewer memo=" + id + " from=open to=discarded",
		`park.failed op=set session=viewer memo=missing reason="メモ missing が無い"`,
		`park.failed op=set session=viewer memo=` + id + ` reason="メモ ` + id + ` の status は discarded (期待 open) なので書き換えない"`,
		"park.adopt session=viewer from_session=dead task=4 memo=" + adopted.Item.ID + " created=true",
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("title / detail / subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
}

func runBoardLog(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newBoardLogCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// board-log は mod の出来事をボードのログへ書く。
func TestBoardLog(t *testing.T) {
	root := parkTestEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	if err := runBoardLog(t, "--session", "s1", "--", "mod.board.failed", "reason=exit 1"); err != nil {
		t.Fatalf("board-log: %v", err)
	}
	if err := runBoardLog(t, "--session", "s1", "--", "Bad Event"); err == nil {
		t.Errorf("出来事の名前でないものを通した")
	}
	if err := runBoardLog(t, "--session", "s2", "--", "mod.hint.failed", "reason=x"); err != nil {
		t.Fatal(err)
	}
	equalLines(t, boardLines(t, state), []string{`mod.board.failed session=s1 reason="exit 1"`, "mod.hint.failed session=s2 reason=x"})
}

// ingest-task と park は、記録した結果をボードのログに書く。subject と title は書かない。
func TestIngestTaskAndParkWriteBoardLog(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	runIngestTask(t, root, taskCreateInput(cwd))
	if _, _, err := runPark(t, parkInput, "add", "--cwd", cwd); err != nil {
		t.Fatal(err)
	}

	log := strings.Join(boardLines(t, filepath.Join(root, "state", "agents-daemon")), "\n")
	for _, want := range []string{"ingest-task session=s1 tool=TaskCreate written=1 removed=0", "park.add session=s1 memo="} {
		if !strings.Contains(log, want) {
			t.Errorf("board.log に %q が無い:\n%s", want, log)
		}
	}
	if strings.Contains(log, "alpha") || strings.Contains(log, "保持期間") {
		t.Errorf("subject か title を書いた:\n%s", log)
	}
}
