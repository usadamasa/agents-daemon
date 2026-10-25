package eventlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- テストの一時ディレクトリ配下
	if err != nil {
		t.Fatalf("%s が読めない: %v", filepath.Base(path), err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// realClock は今の時刻に止めた時計と、その RFC 3339 の表記を返す。write は書くたびに開き直し、既存のファイルの
// mtime (実時刻) から日付の境目を見るので、実時刻と違う日の時計では rotate が走る。
func realClock() (func() time.Time, string) {
	fixed := time.Now().UTC().Truncate(time.Second)
	return func() time.Time { return fixed }, fixed.Format(time.RFC3339)
}

func TestPath(t *testing.T) {
	if got, want := Path("/s"), filepath.Join("/s", "logs", "events.log"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// 1 行 1 件で、時刻、出来事、key=value の順に書く。
func TestWrite_oneLinePerEvent(t *testing.T) {
	dir := t.TempDir()
	now, ts := realClock()

	write(dir, now, "park.add", f("session", "S1"), f("memo", "20261019T090000-abcd"), f("created", true), f("count", 2))
	write(dir, now, "park.set", f("memo", "m"), f("from", "open"), f("to", "handed"))

	got := readLines(t, Path(dir))
	want := []string{
		ts + " park.add session=S1 memo=20261019T090000-abcd created=true count=2",
		ts + " park.set memo=m from=open to=handed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// 空白、改行、= を含む値は引用して 1 行に保つ。
func TestWrite_quotesValues(t *testing.T) {
	dir := t.TempDir()
	now, ts := realClock()

	write(dir, now, "mod.deps.failed", f("reason", "exit 1\nstatus snapshot"), f("x", "a=b"), f("empty", ""), f("pct", "100%"))

	got := readLines(t, Path(dir))
	want := ts + ` mod.deps.failed reason="exit 1\nstatus snapshot" x="a=b" empty="" pct=100%`
	if len(got) != 1 || got[0] != want {
		t.Errorf("lines = %q, want [%q]", got, want)
	}
}

func TestWrite_rotatesOnDayChange(t *testing.T) {
	dir := t.TempDir()
	write(dir, func() time.Time { return time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC) }, "a")
	path := Path(dir)
	day := time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, day, day); err != nil {
		t.Fatal(err)
	}
	write(dir, func() time.Time { return time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC) }, "b")

	if got := readLines(t, path+".2026-10-18"); len(got) != 1 || !strings.HasSuffix(got[0], " a") {
		t.Errorf("退避したファイル = %q", got)
	}
	if got := readLines(t, path); len(got) != 1 || !strings.HasSuffix(got[0], " b") {
		t.Errorf("現行のファイル = %q", got)
	}
}

func TestParseFields(t *testing.T) {
	got, err := parseFields([]string{"rows=2", "reason=exit 1", "x=a=b"})
	if err != nil {
		t.Fatal(err)
	}
	want := []field{{"rows", "2"}, {"reason", "exit 1"}, {"x", "a=b"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	for _, bad := range [][]string{{"noequal"}, {"=v"}, {"bad key=v"}} {
		if _, err := parseFields(bad); err == nil {
			t.Errorf("parseFields(%q) が通った", bad)
		}
	}
}

func TestValidEvent(t *testing.T) {
	for _, ok := range []string{"mod.deps.open", "ingest-task", "park.failed"} {
		if !validEvent(ok) {
			t.Errorf("validEvent(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "Mod", "a b", "a\nb", "-x"} {
		if validEvent(bad) {
			t.Errorf("validEvent(%q) = true", bad)
		}
	}
}
