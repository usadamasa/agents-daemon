package sessionstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNew_ディレクトリ名がシェル側の定数と一致する は protocol の固定｡
// compact-state / compacted の書き出し側はこの plugin の skill・hook (シェル) で､
// 名前が片方だけ変わると daemon が黙って何も読めなくなる｡rate-limits / context は
// 書き手 (IngestStatusline) もこのパッケージにあるが､setup skill の確認手順が
// パスを直に書くので同じく固定する｡
func TestNew_ディレクトリ名がシェル側の定数と一致する(t *testing.T) {
	s := New("/state")

	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{"RateLimits", s.RateLimits, "/state/rate-limits"},
		{"Context", s.Context, "/state/context"},
		{"CompactState", s.CompactState, "/state/compact-state"},
		{"Compacted", s.Compacted, "/state/compacted"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestRateLimitFile_不正なsessionIDを拒否する(t *testing.T) {
	s := New("/state")

	t.Run("パス区切りを含む session ID は拒否する", func(t *testing.T) {
		if _, err := s.RateLimitFile("../../etc/passwd"); err == nil {
			t.Error("パス区切りを含む session ID が通ってしまった")
		}
	})

	t.Run("空の session ID は空パスとして返す (判断材料無し)", func(t *testing.T) {
		path, err := s.RateLimitFile("")
		if err != nil || path != "" {
			t.Errorf("RateLimitFile(\"\") = %q, %v, want \"\", nil", path, err)
		}
	})
}

// fresh は IsFresh と ContextFresh の共通部｡未来の観測を拒むことが要点で､
// 拒まないとクロックスキューのある state で送信が起きる｡
func Test_fresh(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	maxAge := 5 * time.Minute

	for _, tt := range []struct {
		name       string
		observedAt time.Time
		want       bool
	}{
		{"maxAge 以内は fresh", now.Add(-time.Minute), true},
		{"境界ちょうどは fresh", now.Add(-maxAge), true},
		{"maxAge を超えたら古い", now.Add(-maxAge - time.Second), false},
		{"未来の観測は信用しない", now.Add(time.Second), false},
	} {
		if got := fresh(tt.observedAt, now, maxAge); got != tt.want {
			t.Errorf("%s: fresh() = %v, want %v", tt.name, got, tt.want)
		}
	}
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
