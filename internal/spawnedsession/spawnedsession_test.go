package spawnedsession

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	sessA = "session:01a1ca60-c5fc-7a39-9ab4-c22c953d9106"
	sessB = "session:01a1ca60-c767-7aaa-857b-7e167825e668"
)

func ids(t *testing.T, s Store) []string {
	t.Helper()
	recs, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.SessionID)
	}
	slices.Sort(out)
	return out
}

// 印を書くと List に出て、同じ session の印は 1 つにまとまる。消すと出なくなり、無い印を消しても失敗しない。
func TestStore_PutListRemove(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{sessA, sessB, sessA} {
		if err := s.Put(id); err != nil {
			t.Fatalf("Put(%q) error = %v", id, err)
		}
	}
	if got, want := ids(t, s), []string{sessA, sessB}; !slices.Equal(got, want) {
		t.Fatalf("List() = %q, want %q", got, want)
	}
	if err := s.Remove(sessA); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := s.Remove(sessA); err != nil {
		t.Fatalf("2 回目の Remove() error = %v, want nil", err)
	}
	if got, want := ids(t, s), []string{sessB}; !slices.Equal(got, want) {
		t.Fatalf("Remove 後の List() = %q, want %q", got, want)
	}
}

// session ID の無い印は書かない。置き場の無い Store は書けず、読むと空になる。
func TestStore_Rejects(t *testing.T) {
	if err := New(t.TempDir()).Put(""); err == nil {
		t.Error("Put(\"\") error = nil, want error")
	}
	var zero Store
	if err := zero.Put(sessA); err == nil {
		t.Error("ゼロ値の Put() error = nil, want error")
	}
	if got := ids(t, zero); len(got) != 0 {
		t.Errorf("ゼロ値の List() = %q, want 空", got)
	}
}

// 置き場が無ければ空。壊れたファイルと書きかけの一時ファイルは読み飛ばす。
func TestStore_ListSkips(t *testing.T) {
	stateDir := t.TempDir()
	s := New(stateDir)
	if got := ids(t, s); len(got) != 0 {
		t.Fatalf("置き場の無い List() = %q, want 空", got)
	}
	if err := s.Put(sessA); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	for name, content := range map[string]string{"broken.json": "{", ".spawned.123": `{"session_id":"x"}`, "empty.json": `{}`} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := ids(t, s), []string{sessA}; !slices.Equal(got, want) {
		t.Fatalf("List() = %q, want %q", got, want)
	}
}
