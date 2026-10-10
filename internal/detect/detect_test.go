package detect

import (
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	newC := func(t *testing.T, extraLimit, extraTransient []string) *Classifier {
		t.Helper()
		c, err := NewClassifier(extraLimit, extraTransient)
		if err != nil {
			t.Fatalf("NewClassifier() エラー: %v", err)
		}
		return c
	}

	t.Run("実機で採取した通常のフッターは none", func(t *testing.T) {
		screen := strings.Join([]string{
			`  Bash(herdr pane read "$HERDR_PANE_ID" --source detection --lines 12)`,
			``,
			`· Tinkering… (2m 52s · ↓ 9.0k tokens · thought for 8s)`,
			`  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's current work`,
			`                                                                          105021 tokens`,
			`────────────────────────────────────────────────────────────────────────────`,
			`❯`,
			`────────────────────────────────────────────────────────────────────────────`,
			`  🌸 Opus 5 💎 ･ﾟ 💰 $1.60 ･ﾟ 🧠 104k (10%) ･ﾟ ⚡ high ･ﾟ 🎀 Mikawa ･ﾟ 🐣 v2.1.224`,
			`  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`,
		}, "\n")

		c := newC(t, nil, nil)
		if got := c.Classify(screen, 12); got != KindNone {
			t.Errorf("Classify() = %v, want %v (実機フッターの誤検知)", got, KindNone)
		}
	})

	t.Run("上限フレーズとリセット時刻フレーズが近傍にあれば limit", func(t *testing.T) {
		screen := "Claude usage limit reached. Your limit will reset at 3pm."
		c := newC(t, nil, nil)
		if got := c.Classify(screen, 0); got != KindLimit {
			t.Errorf("Classify() = %v, want %v", got, KindLimit)
		}
	})

	t.Run("使用率の警告 (85% of your 5-hour limit) は limit と誤検知しない", func(t *testing.T) {
		screen := strings.Join([]string{
			"· Approaching your limit — 85% of your 5-hour limit",
			"  Resets at 3pm",
		}, "\n")
		c := newC(t, nil, nil)
		if got := c.Classify(screen, 0); got != KindNone {
			t.Errorf("Classify() = %v, want %v (使用率警告の誤検知)", got, KindNone)
		}
	})

	t.Run("上限フレーズ単体 (近傍にリセット時刻が無い) では limit と判定しない", func(t *testing.T) {
		screen := strings.Join([]string{
			"session limit",
			strings.Repeat("no reset info here\n", 10),
		}, "\n")
		c := newC(t, nil, nil)
		if got := c.Classify(screen, 0); got != KindNone {
			t.Errorf("Classify() = %v, want %v (2 シグナル要件が働いていない)", got, KindNone)
		}
	})

	t.Run("直近の出力ブロックの transient エラーは検知する", func(t *testing.T) {
		screen := strings.Join([]string{
			"⏺ Running task",
			"⎿  API Error: 500 Internal Server Error",
		}, "\n")
		c := newC(t, nil, nil)
		if got := c.Classify(screen, 0); got != KindTransient {
			t.Errorf("Classify() = %v, want %v", got, KindTransient)
		}
	})

	t.Run("Claude Code の API Error 行なら ⏺ でも ⎿ でも transient と判定する", func(t *testing.T) {
		// 文言は Claude Code 2.1.296 の bundle から採った (接頭辞 "API Error" と
		// "Server is temporarily limiting requests (not your usage limit)" ､
		// "This is a server-side issue, usually temporary — try again in a moment.")｡
		for _, line := range []string{
			`⏺ API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			`⏺ API Error: Overloaded. This is a server-side issue, usually temporary — try again in a moment.`,
			`⏺ API Error: Server is temporarily limiting requests (not your usage limit)`,
			`  ⎿  API Error: 500 Internal Server Error · Retrying in 2s · attempt 1/10`,
			`  ⎿  API Error: 429 the request was rate limited`,
		} {
			c := newC(t, nil, nil)
			if got := c.Classify(line, 0); got != KindTransient {
				t.Errorf("Classify(%q) = %v, want %v", line, got, KindTransient)
			}
		}
	})

	t.Run("API Error の無い行の transient 語句は tool result の文章なので拾わない", func(t *testing.T) {
		// tool result (⎿ の下の継続行) には利用者とツールが書く任意の文章が流れる｡
		for _, screen := range []string{
			"⏺ Bash(gh issue list)\n  ⎿  usadamasa/twigpui#298\tステータスバーが \"rate limited\" と出る",
			"⏺ Bash(kubectl logs api)\n  ⎿  upstream overloaded, shedding load\n     retrying",
			"⏺ Read(docs/runbook.md)\n  ⎿  nginx が internal server error を返したら",
			"⏺ Bash(rg -n 'rate limited' skills/)\n  ⎿  skills/x/SKILL.md:12: 上流が rate limited を返したら待つ",
		} {
			c := newC(t, nil, nil)
			if got := c.Classify(screen, 0); got != KindNone {
				t.Errorf("Classify(%q) = %v, want %v (tool result の文章を transient と誤検知)", screen, got, KindNone)
			}
		}
	})

	t.Run("API Error でも transient の理由が無い行は拾わない", func(t *testing.T) {
		for _, line := range []string{
			"⏺ API Error: 400 due to tool use concurrency issues.",
			"⏺ API Error: 401 Invalid API key · Please run /login",
			"⏺ API Error: Request was aborted.",
		} {
			c := newC(t, nil, nil)
			if got := c.Classify(line, 0); got != KindNone {
				t.Errorf("Classify(%q) = %v, want %v", line, got, KindNone)
			}
		}
	})

	t.Run("画面上部の古い transient エラーは直近ブロックでなければ検知しない", func(t *testing.T) {
		screen := strings.Join([]string{
			"⏺ Old task",
			"⎿  API Error: 500 Internal Server Error",
			"",
			"⏺ New task",
			"⎿  Done successfully",
		}, "\n")
		c := newC(t, nil, nil)
		if got := c.Classify(screen, 0); got != KindNone {
			t.Errorf("Classify() = %v, want %v (古いエラーの再発火)", got, KindNone)
		}
	})

	t.Run("tailLines がスクロールバックの古い limit 文言を除外する", func(t *testing.T) {
		lines := make([]string, 0, 20)
		lines = append(lines, "usage limit", "resets at 3pm")
		for i := 0; i < 15; i++ {
			lines = append(lines, "normal output line")
		}
		screen := strings.Join(lines, "\n")

		c := newC(t, nil, nil)
		if got := c.Classify(screen, 5); got != KindNone {
			t.Errorf("Classify() = %v, want %v (tailLines で除外されるはず)", got, KindNone)
		}
		if got := c.Classify(screen, 0); got != KindLimit {
			t.Errorf("Classify(tailLines=0) = %v, want %v (全行対象なら検知するはず)", got, KindLimit)
		}
	})

	t.Run("設定由来の追加 limit パターンは組み込みと同じ近傍ルールに従う", func(t *testing.T) {
		screen := strings.Join([]string{
			"account cap hit",
			"resets in: 90",
		}, "\n")
		c := newC(t, []string{"account cap hit"}, nil)
		if got := c.Classify(screen, 0); got != KindLimit {
			t.Errorf("Classify() = %v, want %v (追加 limit パターンが効いていない)", got, KindLimit)
		}
	})

	t.Run("設定由来の追加 limit パターンでも近傍にリセット時刻が無ければ発火しない", func(t *testing.T) {
		screen := "account cap hit, nothing else here"
		c := newC(t, []string{"account cap hit"}, nil)
		if got := c.Classify(screen, 0); got != KindNone {
			t.Errorf("Classify() = %v, want %v (2 シグナル要件が追加パターンに適用されていない)", got, KindNone)
		}
	})

	t.Run("設定由来の追加 transient パターンは API Error 行で効く", func(t *testing.T) {
		screen := strings.Join([]string{
			"⏺ Running task",
			"⎿  API Error: custom backend hiccup",
		}, "\n")
		c := newC(t, nil, []string{"custom backend hiccup"})
		if got := c.Classify(screen, 0); got != KindTransient {
			t.Errorf("Classify() = %v, want %v (追加 transient パターンが効いていない)", got, KindTransient)
		}
	})

	t.Run("設定由来の追加 transient パターンでも API Error の無い行では発火しない", func(t *testing.T) {
		screen := strings.Join([]string{
			"⏺ Bash(cat backend.log)",
			"⎿  custom backend hiccup",
		}, "\n")
		c := newC(t, nil, []string{"custom backend hiccup"})
		if got := c.Classify(screen, 0); got != KindNone {
			t.Errorf("Classify() = %v, want %v (API Error の要件が追加パターンに適用されていない)", got, KindNone)
		}
	})

	t.Run("不正な正規表現は構築時にエラーになる", func(t *testing.T) {
		if _, err := NewClassifier([]string{"("}, nil); err == nil {
			t.Fatal("NewClassifier() エラーが返らなかった (不正な正規表現のはず)")
		}
	})
}

func TestParseRelativeWait(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		tailLines int
		wantDur   time.Duration
		wantOK    bool
	}{
		{"分の表現", "please try again in 12 minutes", 0, 12 * time.Minute, true},
		{"時間の表現", "resets in 2 hours", 0, 2 * time.Hour, true},
		{"省略形 h", "wait 2h and retry", 0, 2 * time.Hour, true},
		{"該当なし", "everything is fine", 0, 0, false},
		{"tailLines でスクロールバックの古い表現を除外", "try again in 12 minutes\n" + strings.Repeat("noise\n", 10), 3, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dur, ok := ParseRelativeWait(c.text, c.tailLines)
			if ok != c.wantOK {
				t.Fatalf("ParseRelativeWait() ok = %v, want %v", ok, c.wantOK)
			}
			if ok && dur != c.wantDur {
				t.Errorf("ParseRelativeWait() dur = %v, want %v", dur, c.wantDur)
			}
		})
	}
}
