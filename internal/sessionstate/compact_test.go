package sessionstate

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCompact(t *testing.T) {
	t.Run("3 つの state を読む", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, filepath.Join(s.Context, "sess-1.json"), `{"session_id":"sess-1","used_percentage":72.5,"observed_at":1786834771}`)
		writeFile(t, filepath.Join(s.CompactState, "sess-1.md"), "# 作業中の状態\n")
		writeFile(t, filepath.Join(s.Compacted, "sess-1"), "")

		state, err := s.LoadCompact("sess-1")
		if err != nil {
			t.Fatalf("LoadCompact() error = %v", err)
		}
		if !state.HasContext || state.UsedPercentage != 72.5 {
			t.Errorf("context = %v / %v, want true / 72.5", state.HasContext, state.UsedPercentage)
		}
		if got, want := state.ObservedAt, time.Unix(1786834771, 0); !got.Equal(want) {
			t.Errorf("ObservedAt = %v, want %v", got, want)
		}
		if !state.HasPrep || state.PrepWrittenAt.IsZero() {
			t.Errorf("prep = %v / %v, want true / 非ゼロ", state.HasPrep, state.PrepWrittenAt)
		}
		if !state.HasCompacted || state.CompactedAt.IsZero() {
			t.Errorf("compacted = %v / %v, want true / 非ゼロ", state.HasCompacted, state.CompactedAt)
		}
	})

	t.Run("ファイルが無いのは正常系で Has* が false になるだけ", func(t *testing.T) {
		s := New(t.TempDir())
		state, err := s.LoadCompact("sess-1")
		if err != nil {
			t.Fatalf("LoadCompact() error = %v", err)
		}
		if state == nil {
			t.Fatal("LoadCompact() = nil, want 空の state")
		}
		if state.HasContext || state.HasPrep || state.HasCompacted {
			t.Errorf("state = %+v, want 全て false", state)
		}
	})

	t.Run("別セッションの state を拾わない", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, filepath.Join(s.Context, "other.json"), `{"used_percentage":99,"observed_at":1}`)

		state, err := s.LoadCompact("sess-1")
		if err != nil {
			t.Fatalf("LoadCompact() error = %v", err)
		}
		if state.HasContext {
			t.Error("別セッションの context を拾ってしまっている")
		}
	})

	t.Run("session ID が空なら判断材料無しとして (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		state, err := s.LoadCompact("")
		if err != nil || state != nil {
			t.Errorf("LoadCompact(\"\") = %+v, %v, want nil, nil", state, err)
		}
	})

	t.Run("パス区切りを含む session ID を拒否する", func(t *testing.T) {
		s := New(t.TempDir())
		if _, err := s.LoadCompact("../../etc/passwd"); err == nil {
			t.Error("パス区切りを含む session ID が通ってしまった")
		}
	})

	t.Run("壊れた JSON はエラーにする", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, filepath.Join(s.Context, "sess-1.json"), `{"used_percentage":`)

		if _, err := s.LoadCompact("sess-1"); err == nil {
			t.Error("壊れた JSON でエラーが返らなかった")
		}
	})

	t.Run("idle compact の marker を読む", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.MarkIdleCompacted("sess-1"); err != nil {
			t.Fatalf("MarkIdleCompacted() error = %v", err)
		}

		state, err := s.LoadCompact("sess-1")
		if err != nil {
			t.Fatalf("LoadCompact() error = %v", err)
		}
		if !state.HasIdleCompacted || state.IdleCompactedAt.IsZero() {
			t.Errorf("idle compacted = %v / %v, want true / 非ゼロ", state.HasIdleCompacted, state.IdleCompactedAt)
		}
	})
}

func TestMarkIdleCompacted(t *testing.T) {
	t.Run("session ID が空なら何もしない", func(t *testing.T) {
		if err := New(t.TempDir()).MarkIdleCompacted(""); err != nil {
			t.Errorf("MarkIdleCompacted(\"\") error = %v", err)
		}
	})

	t.Run("パス区切りを含む session ID を拒否する", func(t *testing.T) {
		if err := New(t.TempDir()).MarkIdleCompacted("../x"); err == nil {
			t.Error("パス区切りを含む session ID が通ってしまった")
		}
	})
}

func TestIdleCompactActive(t *testing.T) {
	marked := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	withMarker := &Compact{HasIdleCompacted: true, IdleCompactedAt: marked}

	for _, tt := range []struct {
		name  string
		state *Compact
		cache *Cache
		want  bool
	}{
		{"nil は false", nil, nil, false},
		{"marker が無ければ false", &Compact{}, nil, false},
		// compact 後に Stop が発火すると､境界の後ろに応答が無いので ingest-stop が sidecar を消す｡
		{"sidecar が無ければ active", withMarker, nil, true},
		{"marker より前の応答なら active", withMarker, &Cache{LastRequestAt: marked.Add(-time.Minute)}, true},
		// 利用者が戻ってターンを終えた｡以後の compact は通常どおり再開の対象にする｡
		{"marker より後の応答があれば解ける", withMarker, &Cache{LastRequestAt: marked.Add(time.Minute)}, false},
	} {
		if got := tt.state.IdleCompactActive(tt.cache); got != tt.want {
			t.Errorf("%s: IdleCompactActive() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestContextFresh(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	for _, tt := range []struct {
		name  string
		state *Compact
		want  bool
	}{
		{"nil は false", nil, false},
		{"context が無ければ false", &Compact{}, false},
		{"maxAge 以内なら true", &Compact{HasContext: true, ObservedAt: now.Add(-time.Minute)}, true},
		{"maxAge を超えたら false", &Compact{HasContext: true, ObservedAt: now.Add(-10 * time.Minute)}, false},
		// IsFresh と揃えた挙動｡クロックスキューのある state で compact を送らない側へ倒す｡
		{"未来の観測は信用しない", &Compact{HasContext: true, ObservedAt: now.Add(time.Second)}, false},
	} {
		if got := tt.state.ContextFresh(now, 5*time.Minute); got != tt.want {
			t.Errorf("%s: ContextFresh() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
