package monitor

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// 起床時刻の情報源は 3 つある: transcript の entry の resetsAt、その pane のセッションの statusline の state (own)、
// アカウント全体で最も新しいウィンドウの state (account)。優先順位は ComputeLimitWake のコメントにあり、
// ここではその順位を表で固定する。

var tokyo = time.FixedZone("Asia/Tokyo", 9*60*60)

func jst(hh, mm int) time.Time {
	return time.Date(2026, 8, 28, hh, mm, 0, 0, tokyo)
}

func stateAt(resetsAt time.Time, used float64) *ratelimit.State {
	return &ratelimit.State{FiveHourUsed: used, FiveHourResetsAt: resetsAt, ObservedAt: resetsAt.Add(-4 * time.Hour)}
}

func TestComputeLimitWake_Priority(t *testing.T) {
	cfg := config.Default()
	margin := time.Duration(cfg.MarginSeconds) * time.Second
	now := jst(7, 35)

	tests := []struct {
		name       string
		entry      *transcript.Entry
		own        *ratelimit.State
		account    *ratelimit.State
		wantWake   time.Time
		wantSource WakeSource
	}{
		{
			name:       "1. transcript の resetsAt が未来ならそれ (statusline より優先)",
			entry:      limitedTurn(uuidLimit, jst(8, 10)).Entry,
			own:        stateAt(jst(13, 10), 100),
			account:    stateAt(jst(13, 10), 5),
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromTranscript,
		},
		{
			name:       "1'. transcript の resetsAt が過去なら今すぐ (解除後に止まったままの pane)",
			entry:      limitedTurn(uuidLimit, jst(3, 10)).Entry,
			own:        stateAt(jst(13, 10), 100),
			account:    stateAt(jst(13, 10), 5),
			wantWake:   now.Add(margin),
			wantSource: WakeTranscriptAlreadyReset,
		},
		{
			name:       "2. resetsAt の無い entry なら own の resets_at (未来)",
			entry:      legacyLimitedTurn().Entry,
			own:        stateAt(jst(8, 10), 100),
			account:    stateAt(jst(13, 10), 5),
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromStatusline,
		},
		{
			name:       "3. own 無し → account の resets_at (上限中に開いた新規セッション)",
			entry:      legacyLimitedTurn().Entry,
			own:        nil,
			account:    stateAt(jst(8, 10), 96),
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromAccount,
		},
		{
			name:       "3'. own が過去 → account の resets_at",
			entry:      legacyLimitedTurn().Entry,
			own:        stateAt(jst(3, 10), 100),
			account:    stateAt(jst(8, 10), 96),
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromAccount,
		},
		{
			name:       "4. own も account も過去 → 今すぐ",
			entry:      legacyLimitedTurn().Entry,
			own:        stateAt(jst(3, 10), 100),
			account:    stateAt(jst(3, 10), 100),
			wantWake:   now.Add(margin),
			wantSource: WakeAlreadyReset,
		},
		{
			name:       "5. どれも無ければ fallback",
			entry:      legacyLimitedTurn().Entry,
			wantWake:   now.Add(time.Duration(cfg.FallbackWaitHours * float64(time.Hour))).Add(margin),
			wantSource: WakeFromFallback,
		},
		{
			name:       "five_hour が null の state は無いものとして扱う",
			entry:      legacyLimitedTurn().Entry,
			own:        &ratelimit.State{ObservedAt: now},
			account:    &ratelimit.State{ObservedAt: now},
			wantWake:   now.Add(time.Duration(cfg.FallbackWaitHours * float64(time.Hour))).Add(margin),
			wantSource: WakeFromFallback,
		},
		{
			name:       "entry が nil でも落ちない (statusline へ)",
			entry:      nil,
			own:        stateAt(jst(8, 10), 100),
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromStatusline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source := ComputeLimitWake(cfg, tt.entry, tt.own, tt.account, now)
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
	sources := []WakeSource{WakeFromTranscript, WakeTranscriptAlreadyReset, WakeFromStatusline, WakeFromAccount, WakeAlreadyReset, WakeFromFallback}
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
