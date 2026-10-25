package eventlog

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// lines はログの行から時刻を落としたもの。ファイルが無ければ nil。
func lines(t *testing.T, stateDir string) []string {
	t.Helper()
	data, err := os.ReadFile(Path(stateDir)) // #nosec G304 -- テストの一時ディレクトリ配下
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

// Record は session を先頭に添えて書き、出来事の名前と引数の形を確かめる。
func TestRecord(t *testing.T) {
	dir := t.TempDir()
	if err := Record(dir, "s1", "mod.deps.failed", []string{"reason=exit 1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, "", "ingest-task.failed", []string{"reason=x"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	want := []string{`mod.deps.failed session=s1 reason="exit 1"`, "ingest-task.failed reason=x"}
	if got := lines(t, dir); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if err := Record(dir, "s1", "Bad Event", nil, time.Now()); err == nil {
		t.Errorf("出来事の名前でないものを通した")
	}
	if err := Record(dir, "s1", "mod.x", []string{"novalue"}, time.Now()); err == nil {
		t.Errorf("key=value でない引数を通した")
	}
	if got := lines(t, dir); len(got) != 2 {
		t.Errorf("通さなかった出来事を書いた: %q", got)
	}
}
