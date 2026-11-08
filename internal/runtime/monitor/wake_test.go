package monitor

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// 起床時刻の情報源は transcript の entry の resetsAt だけ。未来ならそれ + margin、過去なら今 + margin。

var tokyo = time.FixedZone("Asia/Tokyo", 9*60*60)

func jst(hh, mm int) time.Time {
	return time.Date(2026, 8, 28, hh, mm, 0, 0, tokyo)
}

func TestComputeLimitWake(t *testing.T) {
	cfg := config.Default()
	margin := time.Duration(cfg.MarginSeconds) * time.Second
	now := jst(7, 35)

	tests := []struct {
		name       string
		entry      *transcript.Entry
		wantWake   time.Time
		wantSource WakeSource
	}{
		{"resetsAt が未来ならそれ + margin", limitedTurn(uuidLimit, jst(8, 10)).Entry, jst(8, 10).Add(margin), WakeFromTranscript},
		{"resetsAt が過去なら今 + margin (解除後に止まったままの pane)", limitedTurn(uuidLimit, jst(3, 10)).Entry, now.Add(margin), WakeTranscriptAlreadyReset},
		{"resetsAt の無い entry は来ないが、来ても即起床として扱う", legacyLimitedTurn(now).Entry, now.Add(margin), WakeTranscriptAlreadyReset},
		{"entry が nil でも落ちない", nil, now.Add(margin), WakeTranscriptAlreadyReset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source := ComputeLimitWake(cfg, tt.entry, now)
			if source != tt.wantSource {
				t.Errorf("source = %v, want %v", source, tt.wantSource)
			}
			if !got.Equal(tt.wantWake) {
				t.Errorf("wake = %v, want %v", got, tt.wantWake)
			}
		})
	}
}

func TestWakeSource_String_AllDistinct(t *testing.T) {
	sources := []WakeSource{WakeFromTranscript, WakeTranscriptAlreadyReset}
	seen := map[string]WakeSource{}
	ids := map[string]WakeSource{}
	for _, s := range sources {
		str := s.String()
		if str == "不明" || str == "" {
			t.Errorf("%d の String() が %q (説明が無い)", s, str)
		}
		if prev, dup := seen[str]; dup {
			t.Errorf("%d と %d の String() が同じ %q", prev, s, str)
		}
		seen[str] = s
		if prev, dup := ids[s.ID()]; dup || s.ID() == "unknown" {
			t.Errorf("%d と %d の ID() が同じ %q", prev, s, s.ID())
		}
		ids[s.ID()] = s
	}
}
