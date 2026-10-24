package statefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var pruneNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// writeAged は dir/name を作り、mtime を at にする。
func writeAged(t *testing.T, dir, name string, at time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// stubRemove は removeFile を f に差し替え、テストの終わりに戻す。
func stubRemove(t *testing.T, f func(string) error) {
	t.Helper()
	orig := removeFile
	removeFile = f
	t.Cleanup(func() { removeFile = orig })
}

func TestPruneDir(t *testing.T) {
	cutoff := pruneNow.Add(-24 * time.Hour)

	t.Run("cutoff ちょうどとそれより古い通常ファイルを消し、新しいものとサブディレクトリは残す", func(t *testing.T) {
		dir := t.TempDir()
		boundary := writeAged(t, dir, "boundary.json", cutoff)
		old := writeAged(t, dir, "old.json", cutoff.Add(-time.Hour))
		fresh := writeAged(t, dir, "fresh.json", cutoff.Add(time.Nanosecond))
		nested := writeAged(t, filepath.Join(dir, "sub"), "nested.json", cutoff.Add(-time.Hour))

		removed, err := PruneDir(dir, cutoff)
		if err != nil || removed != 2 {
			t.Fatalf("PruneDir() = %d, %v, want 2, nil", removed, err)
		}
		for _, p := range []string{boundary, old} {
			if exists(t, p) {
				t.Errorf("%s が残っている", filepath.Base(p))
			}
		}
		for _, p := range []string{fresh, nested} {
			if !exists(t, p) {
				t.Errorf("%s が消えた", filepath.Base(p))
			}
		}
	})

	t.Run("空のパスと無いディレクトリは何もしない", func(t *testing.T) {
		for _, dir := range []string{"", filepath.Join(t.TempDir(), "none")} {
			if removed, err := PruneDir(dir, cutoff); err != nil || removed != 0 {
				t.Errorf("PruneDir(%q) = %d, %v, want 0, nil", dir, removed, err)
			}
		}
	})

	t.Run("並行に消されたファイル (ErrNotExist) は数えずに飛ばして続ける", func(t *testing.T) {
		dir := t.TempDir()
		a := writeAged(t, dir, "a.json", cutoff)
		b := writeAged(t, dir, "b.json", cutoff)
		c := writeAged(t, dir, "c.json", cutoff)
		stubRemove(t, func(path string) error {
			if path == b {
				return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
			}
			return os.Remove(path)
		})

		removed, err := PruneDir(dir, cutoff)
		if err != nil || removed != 2 {
			t.Fatalf("PruneDir() = %d, %v, want 2, nil", removed, err)
		}
		for _, p := range []string{a, c} {
			if exists(t, p) {
				t.Errorf("%s が残っている", filepath.Base(p))
			}
		}
	})

	t.Run("Remove の失敗でそこで止め、それまでに消した数を返す", func(t *testing.T) {
		dir := t.TempDir()
		a := writeAged(t, dir, "a.json", cutoff)
		b := writeAged(t, dir, "b.json", cutoff)
		c := writeAged(t, dir, "c.json", cutoff)
		stubRemove(t, func(path string) error {
			if path == b {
				return &os.PathError{Op: "remove", Path: path, Err: os.ErrPermission}
			}
			return os.Remove(path)
		})

		removed, err := PruneDir(dir, cutoff)
		if !errors.Is(err, os.ErrPermission) || removed != 1 {
			t.Fatalf("PruneDir() = %d, %v, want 1, ErrPermission", removed, err)
		}
		if exists(t, a) {
			t.Error("a.json が残っている")
		}
		if !exists(t, c) {
			t.Error("失敗のあとの c.json まで消えた")
		}
	})

	t.Run("走査の失敗は dir を添えて返す", func(t *testing.T) {
		file := writeAged(t, t.TempDir(), "not-a-dir", pruneNow)
		_, err := PruneDir(file, cutoff)
		if err == nil {
			t.Fatal("ファイルを dir に渡して error にならない")
		}
		if want := file + " の走査に失敗"; !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q を含む", err, want)
		}
	})
}
