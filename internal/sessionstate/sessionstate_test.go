package sessionstate

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestNew_ディレクトリ名が固定されている は protocol の固定。setup skill の確認手順と
// architecture.md がパスを直に書くので固定する。
func TestNew_ディレクトリ名が固定されている(t *testing.T) {
	s := New("/state")

	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{"RateLimits", s.RateLimits.Dir, "/state/rate-limits"},
		{"Context", s.Context.Dir, "/state/context"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// envOf は map を getenv の形へ写す。無いキーは空文字列になる (os.Getenv と同じ)。
func envOf(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

// clearMultiplexerEnv はテストを走らせる端末の多重化ツールの環境変数を空にする。
// rex の中で go test を打つと継承した値が context file に混ざるため。
func clearMultiplexerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"REX_SESSION", "REX_BLOCK"} {
		t.Setenv(key, "")
	}
}

func mustPath(t *testing.T, s Store, sessionID string) string {
	t.Helper()
	path, err := s.RateLimits.File(sessionID)
	if err != nil {
		t.Fatalf("RateLimits.File(%q) error = %v", sessionID, err)
	}
	return path
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func ageFile(t *testing.T, path string, age time.Duration, now time.Time) {
	t.Helper()
	at := now.Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
}
