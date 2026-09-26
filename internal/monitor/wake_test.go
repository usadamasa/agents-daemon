package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// 起床時刻の情報源は 3 つある: その pane のセッションの state (own)、画面の絶対時刻、
// アカウント全体で最も新しいウィンドウの state (account)。優先順位は
// ComputeLimitWake のコメントにあり、ここではその順位を表で固定する。
//
// テストの時刻は全て JST で組む。画面の絶対時刻は tz 付き (Asia/Tokyo) で、
// ComputeLimitWake は daemon のローカル tz を使うため、テストは local を差し込む。

var tokyo = time.FixedZone("Asia/Tokyo", 9*60*60)

func jst(hh, mm int) time.Time {
	return time.Date(2026, 8, 28, hh, mm, 0, 0, tokyo)
}

func stateAt(resetsAt time.Time, used float64) *sessionstate.RateLimit {
	return &sessionstate.RateLimit{
		FiveHourUsed:     used,
		FiveHourResetsAt: resetsAt,
		ObservedAt:       resetsAt.Add(-4 * time.Hour),
	}
}

func TestComputeLimitWake_Priority(t *testing.T) {
	cfg := config.Default()
	margin := time.Duration(cfg.MarginSeconds) * time.Second
	now := jst(7, 35)
	screen810 := "⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)"
	screen310 := "⏺ You've hit your session limit · resets 3:10am (Asia/Tokyo)"

	tests := []struct {
		name       string
		own        *sessionstate.RateLimit
		account    *sessionstate.RateLimit
		screen     string
		wantWake   time.Time
		wantSource WakeSource
	}{
		{
			name:       "1. own の resets_at が未来ならそれ (画面や account より優先)",
			own:        stateAt(jst(8, 10), 100),
			account:    stateAt(jst(13, 10), 5),
			screen:     screen310,
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromResetsAt,
		},
		{
			name:       "2. own が過去でも画面の絶対時刻が未来ならそれ (2026-08-28 07:34 の誤射を塞ぐ)",
			own:        stateAt(jst(3, 10), 100),
			account:    stateAt(jst(8, 10), 96),
			screen:     screen810,
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromScreenAbsolute,
		},
		{
			name:       "2'. 画面の絶対時刻が過去なら今すぐ (account が次のウィンドウでも待たない)",
			own:        stateAt(jst(3, 10), 100),
			account:    stateAt(jst(13, 10), 5),
			screen:     screen310,
			wantWake:   now.Add(margin),
			wantSource: WakeScreenAlreadyReset,
		},
		{
			name:       "3. own 無し・画面に絶対時刻無し → account の resets_at (新規セッションの本命)",
			own:        nil,
			account:    stateAt(jst(8, 10), 96),
			screen:     limitAbsoluteScreen,
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromAccount,
		},
		{
			name:       "3'. own が過去・画面に絶対時刻無し → account の resets_at",
			own:        stateAt(jst(3, 10), 100),
			account:    stateAt(jst(8, 10), 96),
			screen:     limitAbsoluteScreen,
			wantWake:   jst(8, 10).Add(margin),
			wantSource: WakeFromAccount,
		},
		{
			name:       "4. own も account も過去 → 今すぐ",
			own:        stateAt(jst(3, 10), 100),
			account:    stateAt(jst(3, 10), 100),
			screen:     limitAbsoluteScreen,
			wantWake:   now.Add(margin),
			wantSource: WakeAlreadyReset,
		},
		{
			name:       "4'. own 無し・account が過去 → 今すぐ (fallback に落とさない)",
			own:        nil,
			account:    stateAt(jst(3, 10), 100),
			screen:     limitAbsoluteScreen,
			wantWake:   now.Add(margin),
			wantSource: WakeAlreadyReset,
		},
		{
			name:       "5. state が無ければ画面の相対表現",
			own:        nil,
			account:    nil,
			screen:     limitAndRelativeScreen,
			wantWake:   now.Add(5 * time.Minute).Add(margin),
			wantSource: WakeFromRelative,
		},
		{
			name:       "6. どれも無ければ fallback",
			own:        nil,
			account:    nil,
			screen:     limitAbsoluteScreen,
			wantWake:   now.Add(time.Duration(cfg.FallbackWaitHours * float64(time.Hour))).Add(margin),
			wantSource: WakeFromFallback,
		},
		{
			name:       "five_hour が null の account は無いものとして扱う",
			own:        nil,
			account:    &sessionstate.RateLimit{ObservedAt: now},
			screen:     limitAndRelativeScreen,
			wantWake:   now.Add(5 * time.Minute).Add(margin),
			wantSource: WakeFromRelative,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source := computeLimitWakeIn(cfg, tt.own, tt.account, tt.screen, now, tokyo)
			if source != tt.wantSource {
				t.Errorf("source = %v, want %v", source, tt.wantSource)
			}
			if !got.Equal(tt.wantWake) {
				t.Errorf("wake = %v, want %v", got, tt.wantWake)
			}
		})
	}
}

// 上限中に開いた新規セッションには state ファイルが無い。2026-08-28 07:35:55 に
// 実際にこの形の pane を検知し、起床時刻が fallback (5 時間後) に落ちた。
// アカウント全体の state を渡せば現在のウィンドウの解除時刻で起きる。
func TestTick_FreshSession_NoOwnState_UsesAccountWindow(t *testing.T) {
	now := jst(7, 35)
	account := stateAt(jst(8, 10), 96)

	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAbsoluteScreen, nil), // 相対表現も am/pm も無い
	}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	deps.Account = account
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)

	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeWaiting)
	}
	want := jst(8, 10).Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !ps.WaitUntil.Equal(want) {
		t.Errorf("WaitUntil = %v, want %v (own state が無くても account のウィンドウで起きるべき)", ps.WaitUntil, want)
	}
}

// 画面の絶対時刻は daemon のローカル tz で読む。ComputeLimitWake (公開版) が
// time.Local を使うことを、tz 付き画面で確かめる (tz 付きなら local に依らない)。
func TestComputeLimitWake_ScreenAbsoluteWithTZ(t *testing.T) {
	cfg := config.Default()
	now := jst(7, 35)
	got, source := ComputeLimitWake(cfg, nil, nil, limitAmPmScreen, now)
	if source != WakeFromScreenAbsolute {
		t.Fatalf("source = %v, want %v", source, WakeFromScreenAbsolute)
	}
	want := jst(8, 10).Add(time.Duration(cfg.MarginSeconds) * time.Second)
	if !got.Equal(want) {
		t.Errorf("wake = %v, want %v", got, want)
	}
}

func TestWakeSource_String_AllDistinct(t *testing.T) {
	sources := []WakeSource{
		WakeFromResetsAt, WakeAlreadyReset, WakeFromScreenAbsolute,
		WakeScreenAlreadyReset, WakeFromAccount, WakeFromRelative, WakeFromFallback,
	}
	seen := map[string]WakeSource{}
	for _, s := range sources {
		str := s.String()
		if str == "不明" || str == "" {
			t.Errorf("%d の String() が %q (説明が無い)", s, str)
		}
		if prev, dup := seen[str]; dup {
			t.Errorf("%d と %d の String() が同じ %q", prev, s, str)
		}
		seen[str] = s
	}
}
