package sessionstate

import (
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLoadRateLimit(t *testing.T) {
	t.Run("正常系: five_hour と observed_at を domain の型へ読む", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{
			"five_hour": {"used_percentage": 14.000000000000002, "resets_at": 1786851000},
			"seven_day": {"used_percentage": 10, "resets_at": 1787025600},
			"observed_at": 1786834771,
			"session_id": "sess-1"
		}`)

		state, err := s.LoadRateLimit("sess-1")
		if err != nil {
			t.Fatalf("LoadRateLimit() error = %v", err)
		}
		if state == nil {
			t.Fatal("LoadRateLimit() = nil")
		}
		if got, want := state.FiveHourUsed, 14.000000000000002; got != want {
			t.Errorf("FiveHourUsed = %v, want %v", got, want)
		}
		if got, want := state.FiveHourResetsAt, time.Unix(1786851000, 0); !got.Equal(want) {
			t.Errorf("FiveHourResetsAt = %v, want %v", got, want)
		}
		if got, want := state.ObservedAt, time.Unix(1786834771, 0); !got.Equal(want) {
			t.Errorf("ObservedAt = %v, want %v", got, want)
		}
		if got, want := state.SessionID, "sess-1"; got != want {
			t.Errorf("SessionID = %q, want %q", got, want)
		}
	})

	t.Run("five_hour が null の JSON は HasFiveHour() が false", func(t *testing.T) {
		// 5 時間ウィンドウが切り替わる瞬間､Claude Code は five_hour を null で渡す
		// (実機で 2026-08-27T18:10:01Z に観測)｡null は Go では 0 に読まれるため､
		// これを「使用率 0% / resets_at が epoch 0」と解釈してはいけない｡
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{
			"five_hour": {"used_percentage": null, "resets_at": null},
			"seven_day": {"used_percentage": 47, "resets_at": 1788235200},
			"observed_at": 1787818203,
			"session_id": "sess-1"
		}`)

		state, err := s.LoadRateLimit("sess-1")
		if err != nil {
			t.Fatalf("LoadRateLimit() error = %v", err)
		}
		if state.HasFiveHour() {
			t.Error("HasFiveHour() = true, want false (five_hour が null)")
		}
	})

	t.Run("使用率 0%% でも resets_at があれば有効", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{
			"five_hour": {"used_percentage": 0, "resets_at": 1786851000},
			"observed_at": 1786834771
		}`)

		state, err := s.LoadRateLimit("sess-1")
		if err != nil {
			t.Fatalf("LoadRateLimit() error = %v", err)
		}
		if !state.HasFiveHour() {
			t.Error("HasFiveHour() = false, want true")
		}
	})

	t.Run("ファイルが無いのは正常系 (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		state, err := s.LoadRateLimit("missing")
		if err != nil {
			t.Fatalf("LoadRateLimit() error = %v, want nil", err)
		}
		if state != nil {
			t.Errorf("LoadRateLimit() = %+v, want nil", state)
		}
	})

	t.Run("session ID が空なら判断材料無しとして (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		state, err := s.LoadRateLimit("")
		if err != nil || state != nil {
			t.Errorf("LoadRateLimit(\"\") = %+v, %v, want nil, nil", state, err)
		}
	})

	t.Run("壊れた JSON は不在と区別してエラーにする", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{"five_hour": {`)

		state, err := s.LoadRateLimit("sess-1")
		if err == nil {
			t.Fatal("エラーが返らなかった (壊れた JSON のはず)")
		}
		if state != nil {
			t.Errorf("state = %+v, want nil (エラー時)", state)
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Error("壊れた JSON のエラーが ErrNotExist として扱われている")
		}
	})
}

func TestRateLimitJudgements(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	t.Run("nil の RateLimit はどの判定も false", func(t *testing.T) {
		var r *RateLimit
		if r.IsFresh(now, time.Minute) || r.HasFiveHour() || r.FiveHourExhausted(0) {
			t.Error("nil の RateLimit が true を返した")
		}
	})

	t.Run("FiveHourExhausted は閾値以上で true", func(t *testing.T) {
		for _, tt := range []struct {
			used, threshold float64
			want            bool
		}{
			{95, 90, true},
			{90, 90, true},
			{89.9, 90, false},
		} {
			r := &RateLimit{FiveHourUsed: tt.used}
			if got := r.FiveHourExhausted(tt.threshold); got != tt.want {
				t.Errorf("FiveHourExhausted(%v) with used %v = %v, want %v", tt.threshold, tt.used, got, tt.want)
			}
		}
	})
}

func TestLatestWindow(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	write := func(t *testing.T, s Store, id string, resetsAt, observedAt time.Time) {
		t.Helper()
		writeFile(t, mustPath(t, s, id), `{
			"five_hour": {"used_percentage": 50, "resets_at": `+itoa(resetsAt.Unix())+`},
			"observed_at": `+itoa(observedAt.Unix())+`,
			"session_id": "`+id+`"
		}`)
	}

	t.Run("resets_at が最大のファイルを返す", func(t *testing.T) {
		s := New(t.TempDir())
		write(t, s, "old", now.Add(time.Hour), now.Add(-time.Minute))
		write(t, s, "new", now.Add(2*time.Hour), now.Add(-time.Minute))

		best, err := s.LatestWindow(now)
		if err != nil {
			t.Fatalf("LatestWindow() error = %v", err)
		}
		if best == nil || best.SessionID != "new" {
			t.Errorf("LatestWindow() = %+v, want session new", best)
		}
	})

	t.Run("resets_at が同じなら observed_at が新しい方", func(t *testing.T) {
		s := New(t.TempDir())
		resetsAt := now.Add(time.Hour)
		write(t, s, "stale", resetsAt, now.Add(-10*time.Minute))
		write(t, s, "recent", resetsAt, now.Add(-time.Minute))

		best, err := s.LatestWindow(now)
		if err != nil {
			t.Fatalf("LatestWindow() error = %v", err)
		}
		if best == nil || best.SessionID != "recent" {
			t.Errorf("LatestWindow() = %+v, want session recent", best)
		}
	})

	t.Run("壊れたファイルと five_hour が null のファイルは飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "broken"), `{"five_hour": {`)
		writeFile(t, mustPath(t, s, "null"), `{"five_hour": {"used_percentage": null, "resets_at": null}, "observed_at": 1}`)
		write(t, s, "good", now.Add(time.Hour), now.Add(-time.Minute))

		best, err := s.LatestWindow(now)
		if err != nil {
			t.Fatalf("LatestWindow() error = %v", err)
		}
		if best == nil || best.SessionID != "good" {
			t.Errorf("LatestWindow() = %+v, want session good", best)
		}
	})

	t.Run("now+6h より先の resets_at は候補にしない", func(t *testing.T) {
		s := New(t.TempDir())
		write(t, s, "bogus", now.Add(7*time.Hour), now.Add(-time.Minute))

		best, err := s.LatestWindow(now)
		if err != nil {
			t.Fatalf("LatestWindow() error = %v", err)
		}
		if best != nil {
			t.Errorf("LatestWindow() = %+v, want nil (あり得ない resets_at)", best)
		}
	})

	t.Run("過去の resets_at しか無くてもそれを返す (解除済みの判断材料になる)", func(t *testing.T) {
		s := New(t.TempDir())
		write(t, s, "past", now.Add(-time.Hour), now.Add(-2*time.Hour))

		best, err := s.LatestWindow(now)
		if err != nil {
			t.Fatalf("LatestWindow() error = %v", err)
		}
		if best == nil || best.SessionID != "past" {
			t.Errorf("LatestWindow() = %+v, want session past", best)
		}
	})

	t.Run("ディレクトリが無ければ nil, nil (statusline が一度も書いていない)", func(t *testing.T) {
		s := New(t.TempDir())
		best, err := s.LatestWindow(now)
		if err != nil || best != nil {
			t.Errorf("LatestWindow() = %+v, %v, want nil, nil", best, err)
		}
	})
}

func mustPath(t *testing.T, s Store, sessionID string) string {
	t.Helper()
	path, err := s.RateLimitFile(sessionID)
	if err != nil {
		t.Fatalf("RateLimitFile(%q) error = %v", sessionID, err)
	}
	return path
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
