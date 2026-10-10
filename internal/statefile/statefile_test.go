package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidSessionID(t *testing.T) {
	for _, ok := range []string{"sess-1", "0f8e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b", "a.b_c"} {
		if !ValidSessionID(ok) {
			t.Errorf("ValidSessionID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "a b", "日本語"} {
		if ValidSessionID(bad) {
			t.Errorf("ValidSessionID(%q) = true (ディレクトリの外へ出られる)", bad)
		}
	}
}

func TestWriteJSONAtomic(t *testing.T) {
	t.Run("親ディレクトリを作り､JSON 1 行と改行を書く｡一時ファイルは残らない", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "sub")
		path := filepath.Join(dir, "x.json")
		if err := WriteJSONAtomic(path, ".x.*", map[string]int{"a": 1}); err != nil {
			t.Fatalf("WriteJSONAtomic() error = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "{\"a\":1}\n" {
			t.Errorf("content = %q", got)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Errorf("ディレクトリに %d 個のエントリ (一時ファイルが残っている)", len(entries))
		}
	})

	t.Run("JSON にできない値はエラー", func(t *testing.T) {
		if err := WriteJSONAtomic(filepath.Join(t.TempDir(), "x.json"), ".x.*", make(chan int)); err == nil {
			t.Error("WriteJSONAtomic() error = nil, want error")
		}
	})
}
