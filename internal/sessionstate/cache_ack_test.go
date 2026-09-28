package sessionstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteCacheAck(t *testing.T) {
	t.Run("値を末尾改行なしでそのまま書く", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.WriteCacheAck("sess-1", "2026-09-27T03:44:05.954Z"); err != nil {
			t.Fatalf("WriteCacheAck() error = %v", err)
		}
		// guard (TTLGuard) が transcript の timestamp と文字列で完全一致させるので､
		// 整形も改行も入れない｡
		got := readFileString(t, filepath.Join(s.CacheAck, "sess-1"))
		if got != "2026-09-27T03:44:05.954Z" {
			t.Errorf("ack = %q, want %q", got, "2026-09-27T03:44:05.954Z")
		}
	})

	t.Run("同じ値でも書き直して mtime を新しくする", func(t *testing.T) {
		now := time.Now()
		s := New(t.TempDir())
		path := filepath.Join(s.CacheAck, "sess-1")
		writeFile(t, path, CacheAckUnknown)
		ageFile(t, path, time.Hour, now)

		if err := s.WriteCacheAck("sess-1", CacheAckUnknown); err != nil {
			t.Fatalf("WriteCacheAck() error = %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		// guard は mtime が直近かで daemon の送信かを見る｡
		if info.ModTime().Before(now.Add(-time.Minute)) {
			t.Errorf("mtime = %v, want 書き直した時刻 (%v 付近)", info.ModTime(), now)
		}
	})

	t.Run("session ID が空なら何も書かない", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.WriteCacheAck("", "x"); err != nil {
			t.Fatalf("WriteCacheAck() error = %v", err)
		}
		if _, err := os.Stat(s.CacheAck); !os.IsNotExist(err) {
			t.Errorf("cache-ack/ が作られている (err = %v)", err)
		}
	})

	t.Run("不正な session ID はエラー", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.WriteCacheAck("../etc", "x"); err == nil {
			t.Error("WriteCacheAck() error = nil, want エラー")
		}
	})
}

func TestCache_Expired(t *testing.T) {
	last := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
	c := &Cache{LastRequestAt: last, TTL: time.Hour}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"TTL の手前は warm", last.Add(59 * time.Minute), false},
		{"ちょうど TTL で失効 (now - last >= ttl)", last.Add(time.Hour), true},
		{"TTL を過ぎれば失効", last.Add(5 * time.Hour), true},
		{"last_request_at が未来 (clock skew) なら warm", last.Add(-time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Expired(tc.now); got != tc.want {
				t.Errorf("Expired(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
	if got := c.ExpiresAt(); !got.Equal(last.Add(time.Hour)) {
		t.Errorf("ExpiresAt() = %v, want %v", got, last.Add(time.Hour))
	}
}
