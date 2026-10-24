package statusline

import (
	"testing"
	"time"
)

func TestParseInput(t *testing.T) {
	t.Run("stdin の JSON から描画に要る値を取り出す", func(t *testing.T) {
		in, err := parseInput([]byte(`{
			"model": {"display_name": "Opus 4.7"},
			"workspace": {"current_dir": "/w/proj/sub", "project_dir": "/w/proj", "git_worktree": "wt-feature"},
			"cwd": "/elsewhere",
			"output_style": {"name": "mikawa"},
			"version": "2.1.296",
			"cost": {"total_cost_usd": 0.4242},
			"context_window": {"total_input_tokens": 126543, "used_percentage": 63},
			"session_id": "sess-1",
			"rate_limits": {
				"five_hour": {"used_percentage": 14.000000000000002, "resets_at": 1786851000},
				"seven_day": {"used_percentage": 9.6, "resets_at": 1787025600}
			}
		}`))
		if err != nil {
			t.Fatalf("ParseInput() error = %v", err)
		}
		want := input{
			Model:          "Opus 4.7",
			CurrentDir:     "/w/proj/sub",
			ProjectDir:     "/w/proj",
			Worktree:       "wt-feature",
			OutputStyle:    "mikawa",
			Version:        "2.1.296",
			Cost:           0.4242,
			ContextTokens:  126543,
			ContextPercent: 63,
			SessionID:      "sess-1",
		}
		got := in
		got.FiveHourPercent, got.FiveHourResetsAt, got.SevenDayPercent = nil, nil, nil
		if got != want {
			t.Errorf("ParseInput() = %+v, want %+v", got, want)
		}
		if in.FiveHourPercent == nil || *in.FiveHourPercent != 14 {
			t.Errorf("FiveHourPercent = %v, want 14 (丸める)", in.FiveHourPercent)
		}
		if in.FiveHourResetsAt == nil || !in.FiveHourResetsAt.Equal(time.Unix(1786851000, 0)) {
			t.Errorf("FiveHourResetsAt = %v, want 1786851000", in.FiveHourResetsAt)
		}
		if in.SevenDayPercent == nil || *in.SevenDayPercent != 10 {
			t.Errorf("SevenDayPercent = %v, want 10 (9.6 を丸める)", in.SevenDayPercent)
		}
	})

	t.Run("無い値は statusline.sh と同じ既定になる", func(t *testing.T) {
		in, err := parseInput([]byte(`{}`))
		if err != nil {
			t.Fatalf("ParseInput() error = %v", err)
		}
		if in.Model != "?" {
			t.Errorf("Model = %q, want ?", in.Model)
		}
		if in.CurrentDir != "" || in.Cost != 0 || in.ContextTokens != 0 || in.ContextPercent != 0 {
			t.Errorf("既定が違う: %+v", in)
		}
		if in.FiveHourPercent != nil || in.FiveHourResetsAt != nil || in.SevenDayPercent != nil {
			t.Errorf("rate_limits が無いのに値がある: %+v", in)
		}
	})

	t.Run("current_dir が無ければ cwd を使う", func(t *testing.T) {
		in, err := parseInput([]byte(`{"cwd": "/from-cwd"}`))
		if err != nil {
			t.Fatalf("ParseInput() error = %v", err)
		}
		if in.CurrentDir != "/from-cwd" {
			t.Errorf("CurrentDir = %q, want /from-cwd", in.CurrentDir)
		}
	})

	t.Run("5 時間ウィンドウの切り替わりでは five_hour が null で届く。値なしとして扱う", func(t *testing.T) {
		in, err := parseInput([]byte(`{"rate_limits": {"five_hour": {"used_percentage": null, "resets_at": null}, "seven_day": {"used_percentage": 47, "resets_at": 1788235200}}}`))
		if err != nil {
			t.Fatalf("ParseInput() error = %v", err)
		}
		if in.FiveHourPercent != nil || in.FiveHourResetsAt != nil {
			t.Errorf("five_hour が null なのに値がある: %+v", in)
		}
		if in.SevenDayPercent == nil || *in.SevenDayPercent != 47 {
			t.Errorf("SevenDayPercent = %v, want 47", in.SevenDayPercent)
		}
	})

	t.Run("使用率の丸めは jq の round と同じ (0.5 は 0 から遠い側へ)", func(t *testing.T) {
		in, err := parseInput([]byte(`{"context_window": {"used_percentage": 42.5}, "rate_limits": {"five_hour": {"used_percentage": 79.5}}}`))
		if err != nil {
			t.Fatalf("ParseInput() error = %v", err)
		}
		if in.ContextPercent != 43 {
			t.Errorf("ContextPercent = %d, want 43", in.ContextPercent)
		}
		if in.FiveHourPercent == nil || *in.FiveHourPercent != 80 {
			t.Errorf("FiveHourPercent = %v, want 80", in.FiveHourPercent)
		}
	})

	t.Run("壊れた JSON はエラー", func(t *testing.T) {
		if _, err := parseInput([]byte(`{"session_id": `)); err == nil {
			t.Error("ParseInput() error = nil, want error")
		}
	})
}

func TestModeFor(t *testing.T) {
	tests := []struct {
		columns string
		want    mode
	}{
		{"200", ModeFull},
		{"100", ModeFull},
		{"99", ModeCompact},
		{"60", ModeCompact},
		{"59", ModeHidden},
		{"0", ModeFull},
		{"", ModeFull},
		{"narrow", ModeFull},
	}
	for _, tc := range tests {
		if got := modeFor(tc.columns); got != tc.want {
			t.Errorf("ModeFor(%q) = %v, want %v", tc.columns, got, tc.want)
		}
	}
}
