package statefile

// 外から来た session ID をファイル名にしても dir の外へ出ないことを go の fuzz で確かめる。

import (
	"path/filepath"
	"testing"
)

func FuzzSessionPath(f *testing.F) {
	for _, id := range []string{"", "abc", "..", "../x", "a/b", "a\x00b", "session-01A.B_c"} {
		f.Add("/state/rate-limits", id)
		f.Add("", id)
	}
	f.Fuzz(func(t *testing.T, dir, id string) {
		path, err := SessionPath(dir, id, ".json")
		switch {
		case id == "" && (path != "" || err != nil):
			t.Fatalf("空の ID で (%q, %v)", path, err)
		case err != nil && path != "":
			t.Fatalf("error と path %q を両方返した", path)
		case err == nil && id != "" && filepath.Dir(path) != filepath.Clean(dir):
			t.Fatalf("SessionPath(%q, %q) = %q が dir の外", dir, id, path)
		case err == nil && id != "" && filepath.Base(path) != id+".json":
			t.Fatalf("SessionPath(%q, %q) = %q のファイル名が ID と違う", dir, id, path)
		}
	})
}
