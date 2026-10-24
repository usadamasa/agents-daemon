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
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// boardLines は domain のボードのログの行から時刻を落としたもの。ファイルが無ければ nil。
func boardLines(t *testing.T, stateDir string, d domain.Domain) []string {
	t.Helper()
	data, err := os.ReadFile(boardlog.Path(stateDir, d)) // #nosec G304 -- テストの一時ディレクトリ配下
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
	logIngest(dir, tasklog.Result{Tool: "TaskList", SessionID: "s1", Domain: domain.Home, Written: 2, Removed: 1}, nil, time.Now())
	logIngest(dir, tasklog.Result{Tool: "TaskCreate", SessionID: "s1", AgentID: "a1", Domain: domain.Home, Written: 1}, nil, time.Now())
	// 記録しないツールは書かない。
	logIngest(dir, tasklog.Result{}, nil, time.Now())
	equalLines(t, boardLines(t, dir, domain.Home), []string{
		"ingest-task session=s1 tool=TaskList written=2 removed=1",
		"ingest-task session=s1 agent=a1 tool=TaskCreate written=1 removed=0",
	})

	// 置き場を決める前に失敗したら、業務の側に倒す (私用の側へ出ない向き)。
	logIngest(dir, tasklog.Result{}, errors.New("hook 入力の JSON を読めない"), time.Now())
	logIngest(dir, tasklog.Result{Tool: "TaskCreate", SessionID: "s2", Domain: domain.Home}, errors.New(`task ID "../x" はファイル名に使えない`), time.Now())
	equalLines(t, boardLines(t, dir, domain.Work), []string{`ingest-task.failed reason="hook 入力の JSON を読めない"`})
	if got := boardLines(t, dir, domain.Home); len(got) != 3 || got[2] != `ingest-task.failed session=s2 tool=TaskCreate reason="task ID \"../x\" はファイル名に使えない"` {
		t.Errorf("home = %q", got)
	}
}

// park の操作は置き先の domain のログに書き、メモの title と detail とタスクの subject は書かない。
func TestParkEvents(t *testing.T) {
	dir := t.TempDir()
	s := parking.New(dir)
	p := tasklog.Placer{Tasks: tasklog.New(dir), Classify: func(string) domain.Domain { return domain.Home }}
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
	if _, err := parkSet(dir, s, p, "viewer", "/src/a", added.Item.ID, parking.StatusDiscarded, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := parkSet(dir, s, p, "viewer", "/src/a", "missing", parking.StatusDiscarded, "", now); err == nil {
		t.Fatal("無いメモを書き換えた")
	}
	if _, err := parkSet(dir, s, p, "viewer", "/src/a", added.Item.ID, parking.StatusHanded, parking.StatusOpen, now); err == nil {
		t.Fatal("捨てたメモを --from open で取れた")
	}
	writeTestConfig(t, filepath.Join(dir, "tasks", "home", "dead", "4.json"), `{"id": "4", "subject": "秘密のタスク", "status": "pending", "session_id": "dead", "cwd": "/src/a"}`)
	exited := func(string, time.Time) error { return nil }
	adopted, err := parkAdopt(dir, s, p, "viewer", "/src/a", "dead", "4", exited, now)
	if err != nil {
		t.Fatal(err)
	}

	id := added.Item.ID
	got := boardLines(t, dir, domain.Home)
	equalLines(t, got, []string{
		"park.add session=s1 memo=" + id + " kind=aside created=true",
		"park.add session=s2 memo=" + id + " kind=aside created=false",
		`park.failed op=add reason="stdin の JSON を読めない: unexpected EOF"`,
		"park.set session=viewer memo=" + id + " from=open to=discarded",
		`park.failed op=set session=viewer memo=missing reason="home のメモ missing が無い"`,
		`park.failed op=set session=viewer memo=` + id + ` reason="メモ ` + id + ` の status は discarded (期待 open) なので書き換えない"`,
		"park.adopt session=viewer from_session=dead task=4 memo=" + adopted.Item.ID + " created=true",
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("title / detail / subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
	if boardLines(t, dir, domain.Work) != nil {
		t.Errorf("私用の出来事を業務のログに書いた")
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

// board-log は開いたセッションの domain のログへ書く。業務に倒したセッションは cwd が私用でも業務に書く。
func TestBoardLog(t *testing.T) {
	root := parkTestEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	home := filepath.Join(root, "src", "home", "a")
	if err := runBoardLog(t, "--session", "s1", "--cwd", home, "--", "mod.board.failed", "reason=exit 1"); err != nil {
		t.Fatalf("board-log: %v", err)
	}
	if err := runBoardLog(t, "--session", "s1", "--cwd", home, "--", "Bad Event"); err == nil {
		t.Errorf("出来事の名前でないものを通した")
	}
	writeTaskRecord(t, root, "work", "s2", `{"id": "1", "subject": "a", "status": "pending", "session_id": "s2"}`)
	if err := runBoardLog(t, "--session", "s2", "--cwd", home, "--", "mod.hint.failed", "reason=x"); err != nil {
		t.Fatal(err)
	}
	equalLines(t, boardLines(t, state, domain.Home), []string{`mod.board.failed session=s1 reason="exit 1"`})
	equalLines(t, boardLines(t, state, domain.Work), []string{"mod.hint.failed session=s2 reason=x"})
}

// ingest-task と park は、記録した結果を置き先の domain のボードのログに書く。subject と title は書かない。
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

	log := strings.Join(boardLines(t, filepath.Join(root, "state", "agents-daemon"), domain.Home), "\n")
	for _, want := range []string{"ingest-task session=s1 tool=TaskCreate written=1 removed=0", "park.add session=s1 memo="} {
		if !strings.Contains(log, want) {
			t.Errorf("board-home.log に %q が無い:\n%s", want, log)
		}
	}
	if strings.Contains(log, "alpha") || strings.Contains(log, "保持期間") {
		t.Errorf("subject か title を書いた:\n%s", log)
	}
}
