package statusline

import (
	"strings"
	"testing"
	"time"
)

// fullView は full モードの最小の View。bats の make_input "Opus" "/tmp" に当たる。
func fullView(model, dir string) view {
	return view{input: input{Model: model, CurrentDir: dir}, Mode: ModeFull, Home: "/home/u"}
}

func intp(v int) *int { return &v }

func timep(t time.Time) *time.Time { return &t }

var renderNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// withRateLimits は 5h の使用率とリセットまでの秒数、7d の使用率 (負なら無し) を足す。
func withRateLimits(v view, fiveHour, resetIn, sevenDay int) view {
	v.FiveHourPercent = intp(fiveHour)
	v.FiveHourResetsAt = timep(renderNow.Add(time.Duration(resetIn) * time.Second))
	if sevenDay >= 0 {
		v.SevenDayPercent = intp(sevenDay)
	}
	return v
}

func render(t *testing.T, v view) string {
	t.Helper()
	return v.render(renderNow)
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func assertContains(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("出力に %q が無い:\n%q", want, out)
	}
}

func assertNotContains(t *testing.T, out, unwanted string) {
	t.Helper()
	if strings.Contains(out, unwanted) {
		t.Errorf("出力に %q がある:\n%q", unwanted, out)
	}
}

func TestRender_model(t *testing.T) {
	tests := []struct{ model, want string }{
		{"Opus 4.7", "🌸 Opus 4.7 💎"},
		{"Sonnet 4.6", "🌸 Sonnet 4.6 🎵"},
		{"Haiku 4.5", "🌸 Haiku 4.5 🍃"},
		{"GPT-9", "🌸 GPT-9 🤖"},
		{"claude-opus-4-8[1m]", "💎"},
	}
	for _, tc := range tests {
		assertContains(t, render(t, fullView(tc.model, "/tmp")), tc.want)
	}
}

func TestRender_cost(t *testing.T) {
	v := fullView("Opus", "/tmp")
	v.Cost = 0.42
	assertContains(t, render(t, v), "💰 $0.42")
	assertContains(t, render(t, fullView("Opus", "/tmp")), "💰 $0.00")
}

func TestRender_context(t *testing.T) {
	t.Run("tokens と percentage を表示する", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.ContextTokens, v.ContextPercent = 100000, 50
		assertContains(t, render(t, v), "🧠 100k (50%)")
		assertContains(t, render(t, fullView("Opus", "/tmp")), "🧠 0k (0%)")
	})
	t.Run("使用率で色が変わる (50 / 80)", func(t *testing.T) {
		tests := []struct {
			tokens int64
			pct    int
			want   string
		}{
			{40000, 20, green + "🧠 40k (20%)"},
			{100000, 50, orange + "🧠 100k (50%)"},
			{170000, 85, red + "🧠 170k (85%)"},
		}
		for _, tc := range tests {
			v := fullView("Opus", "/tmp")
			v.ContextTokens, v.ContextPercent = tc.tokens, tc.pct
			assertContains(t, render(t, v), tc.want)
		}
	})
}

func TestRender_outputStyle(t *testing.T) {
	v := fullView("Opus", "/tmp")
	v.OutputStyle = "learning"
	assertContains(t, render(t, v), "🎀 learning")
	v.OutputStyle = "default"
	assertNotContains(t, render(t, v), "🎀")
	assertNotContains(t, render(t, fullView("Opus", "/tmp")), "🎀")
}

func TestRender_effort(t *testing.T) {
	assertNotContains(t, render(t, fullView("Opus", "/tmp")), "⚡")
	tests := []struct{ effort, color string }{
		{"low", gray}, {"medium", sky}, {"high", green}, {"xhigh", orange}, {"max", red},
	}
	for _, tc := range tests {
		v := fullView("Opus", "/tmp")
		v.Effort = tc.effort
		assertContains(t, render(t, v), tc.color+"⚡ "+tc.effort)
	}
}

func TestRender_version(t *testing.T) {
	v := fullView("Opus", "/tmp")
	v.Version = "2.1.81"
	assertContains(t, render(t, v), "🐣 v2.1.81")
	assertNotContains(t, render(t, fullView("Opus", "/tmp")), "🐣")
}

func TestRender_path(t *testing.T) {
	assertContains(t, render(t, fullView("Opus", "/home/u/proj")), "📁 ~/proj")
	assertContains(t, render(t, fullView("Opus", "/var/log")), "📁 /var/log")
}

func TestRender_branch(t *testing.T) {
	v := fullView("Opus", "/w/repo")
	v.Branch = "feature-x"
	assertContains(t, render(t, v), "🌿 feature-x ✨")
	assertNotContains(t, render(t, fullView("Opus", "/w/plain")), "🌿")
}

func TestRender_project(t *testing.T) {
	t.Run("cwd == project_dir なら 📁 は sky で 🏠 無し", func(t *testing.T) {
		v := fullView("Opus", "/w/proj")
		v.ProjectDir = "/w/proj"
		out := render(t, v)
		assertContains(t, out, sky+"📁 /w/proj")
		assertNotContains(t, out, "🏠")
	})
	t.Run("サブディレクトリでも 🏠 無し", func(t *testing.T) {
		v := fullView("Opus", "/w/proj/sub")
		v.ProjectDir = "/w/proj"
		out := render(t, v)
		assertContains(t, out, sky+"📁 /w/proj/sub")
		assertNotContains(t, out, "🏠")
	})
	t.Run("外なら 📁 が orange になり 🏠 に起動元の名前と branch", func(t *testing.T) {
		v := fullView("Opus", "/w/away")
		v.ProjectDir, v.Branch, v.ProjectBranch = "/w/home_proj", "visiting-branch", "launch-branch"
		out := render(t, v)
		assertContains(t, out, orange+"📁 /w/away")
		assertContains(t, out, "🌿 visiting-branch ✨")
		assertContains(t, out, "🏠 home_proj (launch-branch)")
	})
	t.Run("prefix が一致するだけの別 dir は外扱い", func(t *testing.T) {
		v := fullView("Opus", "/w/foobar")
		v.ProjectDir, v.ProjectBranch = "/w/foo", "(detached)"
		out := render(t, v)
		assertContains(t, out, orange+"📁 /w/foobar")
		assertContains(t, out, "🏠 foo ((detached))")
	})
}

func TestRender_worktree(t *testing.T) {
	assertNotContains(t, render(t, fullView("Opus", "/w/repo")), "🌲")
	v := fullView("Opus", "/w/repo")
	v.Worktree = "wt-feature"
	assertContains(t, render(t, v), "🌲 wt-feature")
}

func TestRender_lines(t *testing.T) {
	out := lines(render(t, fullView("Opus", "/tmp")))
	if len(out) != 3 {
		t.Fatalf("出力が %d 行、want 3:\n%q", len(out), out)
	}
	assertContains(t, out[0], "🌸 Opus")
	assertContains(t, out[1], "🧠")
	assertContains(t, out[1], "💰")
	assertContains(t, out[2], "📁")
}

func TestRender_rateLimits(t *testing.T) {
	t.Run("5h 使用率・リセットまでの残り時間・7d 使用率", func(t *testing.T) {
		assertContains(t, render(t, withRateLimits(fullView("Opus", "/tmp"), 42, 6750, 9)), "⏳ 5h 42% (↻1h52m) 7d 9%")
	})
	t.Run("1 時間未満なら分だけ", func(t *testing.T) {
		assertContains(t, render(t, withRateLimits(fullView("Opus", "/tmp"), 42, 2850, 9)), "(↻47m)")
	})
	t.Run("リセット時刻を過ぎていても負にしない", func(t *testing.T) {
		assertContains(t, render(t, withRateLimits(fullView("Opus", "/tmp"), 42, -3600, 9)), "(↻0m)")
	})
	t.Run("seven_day が無ければ 7d を出さない", func(t *testing.T) {
		out := render(t, withRateLimits(fullView("Opus", "/tmp"), 42, 6750, -1))
		assertContains(t, out, "⏳ 5h 42% (↻1h52m)")
		assertNotContains(t, out, "7d")
	})
	t.Run("resets_at が無ければ残り時間を出さない", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.FiveHourPercent = intp(42)
		out := render(t, v)
		assertContains(t, out, "⏳ 5h 42%")
		assertNotContains(t, out, "↻")
	})
	t.Run("five_hour が無く seven_day だけなら 7d を出し、色は 7d の使用率に従う", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.SevenDayPercent = intp(63)
		out := render(t, v)
		assertContains(t, out, orange+"⏳ 7d 63%")
		assertNotContains(t, out, "5h")
	})
	t.Run("rate_limits が無ければ ⏳ を出さない", func(t *testing.T) {
		assertNotContains(t, render(t, fullView("Opus", "/tmp")), "⏳")
	})
	t.Run("5h 使用率で色が変わる (50 / 80)", func(t *testing.T) {
		assertContains(t, render(t, withRateLimits(fullView("Opus", "/tmp"), 49, 600, 9)), green+"⏳")
		assertContains(t, render(t, withRateLimits(fullView("Opus", "/tmp"), 50, 600, 9)), orange+"⏳")
		assertContains(t, render(t, withRateLimits(fullView("Opus", "/tmp"), 80, 600, 9)), red+"⏳")
	})
	t.Run("compact では ⏳ を落とす", func(t *testing.T) {
		v := withRateLimits(fullView("Opus", "/tmp"), 42, 6750, 9)
		v.Mode = ModeCompact
		out := render(t, v)
		if len(lines(out)) != 1 {
			t.Errorf("compact が %d 行", len(lines(out)))
		}
		assertNotContains(t, out, "⏳")
	})
}

func TestRender_modes(t *testing.T) {
	t.Run("compact は 1 行に畳み、model・cost・version・output_style を落とす", func(t *testing.T) {
		v := fullView("Opus", "/w/repo")
		v.OutputStyle, v.Version, v.Cost = "mikawa", "2.1.233", 1.23
		v.ContextTokens, v.ContextPercent = 45000, 12
		v.Effort, v.Branch = "high", "feature-x"
		v.Mode = ModeCompact
		out := render(t, v)
		if len(lines(out)) != 1 {
			t.Fatalf("compact が %d 行:\n%q", len(lines(out)), out)
		}
		for _, want := range []string{"🧠 45k (12%)", "⚡ high", "🌿 feature-x"} {
			assertContains(t, out, want)
		}
		for _, unwanted := range []string{"🌸", "💰", "🐣", "🎀", "✨"} {
			assertNotContains(t, out, unwanted)
		}
	})
	t.Run("compact のディレクトリは末尾 1 要素", func(t *testing.T) {
		v := fullView("Opus", "/w/a/b/target-dir")
		v.Mode = ModeCompact
		out := render(t, v)
		assertContains(t, out, "📁 target-dir")
		assertNotContains(t, out, "/w/a/b")
	})
	t.Run("compact でも worktree 名は残す", func(t *testing.T) {
		v := fullView("Opus", "/w/repo")
		v.Worktree, v.Mode = "wt-feature", ModeCompact
		assertContains(t, render(t, v), "🌲 wt-feature")
	})
	t.Run("hidden は何も出さない", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.Mode = ModeHidden
		if out := render(t, v); out != "" {
			t.Errorf("hidden の出力 = %q, want empty", out)
		}
	})
}

func TestRender_spend(t *testing.T) {
	t.Run("その日を 📅、当月と上限を 🧾 で出す", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.Spend = &spendView{Day: 3.79, Month: 3.79, Cap: 10000}
		out := render(t, v)
		assertContains(t, out, "📅 $3.79")
		assertContains(t, out, "🧾 ~$3.79 / $10k (0%)")
	})
	t.Run("上限が無ければ出さない", func(t *testing.T) {
		out := render(t, fullView("Opus", "/tmp"))
		assertNotContains(t, out, "📅")
		assertNotContains(t, out, "🧾")
	})
	t.Run("当月は別の日のぶんを含む", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.Spend = &spendView{Day: 3.79, Month: 14.29, Cap: 10000}
		out := render(t, v)
		assertContains(t, out, "📅 $3.79")
		assertContains(t, out, "🧾 ~$14.29")
	})
	t.Run("上限に対する割合で色が変わる", func(t *testing.T) {
		for _, tc := range []struct {
			month float64
			color string
		}{{10, green}, {50, orange}, {80, red}} {
			v := fullView("Opus", "/tmp")
			v.Spend = &spendView{Day: tc.month, Month: tc.month, Cap: 100}
			assertContains(t, render(t, v), tc.color+"🧾")
		}
	})
	t.Run("1000 未満の上限はそのまま、1000 以上は k", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.Spend = &spendView{Day: 30, Month: 30, Cap: 300}
		assertContains(t, render(t, v), "/ $300 (10%)")
		v.Spend = &spendView{Day: 30, Month: 30, Cap: 1500}
		assertContains(t, render(t, v), "/ $1.5k (2%)")
	})
	t.Run("compact では出さない", func(t *testing.T) {
		v := fullView("Opus", "/tmp")
		v.Spend = &spendView{Day: 3.79, Month: 3.79, Cap: 10000}
		v.Mode = ModeCompact
		assertNotContains(t, render(t, v), "🧾")
	})
}
