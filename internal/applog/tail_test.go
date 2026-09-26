package applog

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestTailLogLines(t *testing.T) {
	t.Run("ファイルが存在しない場合はエラーにせず空を返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such.log")
		lines, err := TailLogLines(path, 10)
		if err != nil {
			t.Fatalf("TailLogLines() error = %v, want nil (daemon未起動はエラーではない)", err)
		}
		if len(lines) != 0 {
			t.Errorf("lines = %v, want empty", lines)
		}
	})

	t.Run("要求より行数が少ない場合は全行を返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "short.log")
		content := "line1\nline2\nline3\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		lines, err := TailLogLines(path, 10)
		if err != nil {
			t.Fatalf("TailLogLines() error = %v", err)
		}
		want := []string{"line1", "line2", "line3"}
		if !slices.Equal(lines, want) {
			t.Errorf("TailLogLines() = %v, want %v", lines, want)
		}
	})

	t.Run("要求より行数が多い場合は末尾N行のみ返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "long.log")
		var b strings.Builder
		for i := 1; i <= 100; i++ {
			b.WriteString("line")
			b.WriteString(strconv.Itoa(i))
			b.WriteByte('\n')
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		lines, err := TailLogLines(path, 5)
		if err != nil {
			t.Fatalf("TailLogLines() error = %v", err)
		}
		want := []string{"line96", "line97", "line98", "line99", "line100"}
		if !slices.Equal(lines, want) {
			t.Errorf("TailLogLines() = %v, want %v", lines, want)
		}
	})

	t.Run("空ファイルは空を返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.log")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		lines, err := TailLogLines(path, 10)
		if err != nil {
			t.Fatalf("TailLogLines() error = %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("lines = %v, want empty", lines)
		}
	})
}
