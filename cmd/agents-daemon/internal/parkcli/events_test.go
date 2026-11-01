package parkcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// eventLines は出来事のログの行から時刻を落としたもの。ファイルが無ければ nil。
func eventLines(t *testing.T, stateDir string) []string {
	t.Helper()
	data, err := os.ReadFile(eventlog.Path(stateDir)) // #nosec G304 -- テストの一時ディレクトリ配下
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

// park の操作は出来事のログに書き、メモの title と detail とタスクの subject は書かない。
func TestParkEvents(t *testing.T) {
	dir := t.TempDir()
	s := parking.New(dir)
	p := tasklog.New(dir)
	now := time.Now()

	memo := parking.Entry{Kind: parking.KindAside, Title: "秘密の題", Detail: "秘密の詳細"}
	added, err := parkAdd(dir, s, protocol.ParkAddRequest{SessionID: "s1", Memo: memo}, "/src/a", p, now)
	if err != nil || !added.Created {
		t.Fatalf("parkAdd = %+v, %v", added, err)
	}
	if _, err := parkAdd(dir, s, protocol.ParkAddRequest{SessionID: "s2", Memo: parking.Entry{Kind: parking.KindAside, Title: "秘密の題"}}, "/src/a", p, now); err != nil {
		t.Fatal(err)
	}
	if _, err := parkAdd(dir, s, protocol.ParkAddRequest{SessionID: "s3", Memo: parking.Entry{Kind: parking.KindAside}}, "/src/a", p, now); err == nil {
		t.Fatal("title の無いメモが通った")
	}
	if _, err := parkSet(dir, s, "viewer", added.Item.ID, parking.StatusDiscarded, "", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := parkSet(dir, s, "viewer", "missing", parking.StatusDiscarded, "", "", now); err == nil {
		t.Fatal("無いメモを書き換えた")
	}
	if _, err := parkSet(dir, s, "viewer", added.Item.ID, parking.StatusHanded, parking.StatusOpen, parking.ToSession("S2"), now); err == nil {
		t.Fatal("捨てたメモを from open で取れた")
	}
	writeTestConfig(t, filepath.Join(dir, "tasks", "dead", "4.json"), `{"id": "4", "subject": "秘密のタスク", "status": "pending", "session_id": "dead", "cwd": "/src/a"}`)
	exited := func(string, time.Time) error { return nil }
	adopted, err := parkAdopt(dir, s, p, "viewer", "dead", "4", exited, now)
	if err != nil {
		t.Fatal(err)
	}

	id := added.Item.ID
	got := eventLines(t, dir)
	equalLines(t, got, []string{
		"park.add session=s1 memo=" + id + " kind=aside created=true",
		"park.add session=s2 memo=" + id + " kind=aside created=false",
		`park.failed op=add session=s3 reason="title が空"`,
		"park.set session=viewer memo=" + id + " from=open to=discarded",
		`park.failed op=set session=viewer memo=missing reason="メモ missing が無い"`,
		`park.failed op=set session=viewer memo=` + id + ` reason="メモ ` + id + ` の status は discarded (期待 open) なので書き換えない"`,
		"park.adopt session=viewer from_session=dead task=4 memo=" + adopted.Item.ID + " created=true",
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("title / detail / subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
}

// park withdraw は取り下げたメモと、取り下げられなかった理由を書く。
func TestParkWithdrawEvents(t *testing.T) {
	dir := t.TempDir()
	s := parking.New(dir)
	now := time.Now()
	added, err := parkAdd(dir, s, protocol.ParkAddRequest{SessionID: "s1", Memo: parking.Entry{Kind: parking.KindScope, Title: "秘密の題"}}, "/src/a", tasklog.New(dir), now)
	if err != nil {
		t.Fatal(err)
	}
	id := added.Item.ID
	if _, err := parkWithdraw(dir, s, "s2", id, now); err == nil {
		t.Fatal("置いていないセッションが取り下げた")
	}
	if _, err := parkWithdraw(dir, s, "s1", id, now); err != nil {
		t.Fatal(err)
	}
	got := eventLines(t, dir)
	equalLines(t, got[1:], []string{
		`park.failed op=withdraw session=s2 memo=` + id + ` reason="メモ ` + id + ` はこのセッションだけが置いたメモではないので取り下げない。依存図の pane の Discard で捨てる"`,
		"park.withdraw session=s1 memo=" + id,
	})
}

// park add は置いた結果と、読めなかった入力を出来事のログに書く。title は書かない。
func TestParkWritesEventLog(t *testing.T) {
	root := parkTestEnv(t)
	cwd := filepath.Join(root, "src", "home", "a")
	if err := os.MkdirAll(cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	add(t, addReq(t, "s1", cwd, memoTitle))
	if _, _, err := runPark(t, "add", `{"memo": `); err == nil {
		t.Fatal("壊れた JSON が通った")
	}

	log := strings.Join(eventLines(t, filepath.Join(root, "state", "agents-daemon")), "\n")
	for _, want := range []string{"park.add session=s1 memo=", `park.failed op=add reason="stdin の JSON を読めない: unexpected end of JSON input"`} {
		if !strings.Contains(log, want) {
			t.Errorf("events.log に %q が無い:\n%s", want, log)
		}
	}
	if strings.Contains(log, "保持期間") {
		t.Errorf("title を書いた:\n%s", log)
	}
}
