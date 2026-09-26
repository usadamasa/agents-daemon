package detect

import (
	"testing"
	"time"
)

// 2026-08-16 に実機 (Claude Code 2.1.224 / サブスクリプション契約) で 5 時間ウィンドウの
// 上限に到達したときの実際の表示｡推測ではなく現物なので､文言が変わったことに
// 気づくための基準として残す｡
//
//	You've hit your session limit · resets 12:30pm (Asia/Tokyo)
//	/upgrade to increase your usage limit.
//
// 重要なのは limit 文言と reset 文言が同じ 1 行に同居している点｡二段構え
// (limit 文言 + 近くの reset 文言) の要件はこれで満たされる｡
const realLimitScreen = `  ⏺ Bash(task test)
  ⎿  ✅ テスト完了

⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)
  /upgrade to increase your usage limit.

────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────
  🌸 Opus 5 💎 ･ﾟ 💰 $22.10 ･ﾟ 🧠 229k (23%) ･ﾟ ⚡ high ･ﾟ 🎀 Mikawa ･ﾟ 🐣 v2.1.224
  📁 ~/src/github.com/usadamasa/claude-config ･ﾟ 🌿 feat/claude-auto-retry ✨
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
`

func TestClassify_実機で観測した上限到達画面(t *testing.T) {
	c, err := NewClassifier(nil, nil)
	if err != nil {
		t.Fatalf("NewClassifier: %v", err)
	}

	t.Run("フッター 15 行の走査で limit と判定する", func(t *testing.T) {
		if got := c.Classify(realLimitScreen, 15); got != KindLimit {
			t.Errorf("Classify() = %v, want %v", got, KindLimit)
		}
	})

	t.Run("該当行だけでも limit と判定する", func(t *testing.T) {
		line := "⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)"
		if got := c.Classify(line, 0); got != KindLimit {
			t.Errorf("Classify() = %v, want %v", got, KindLimit)
		}
	})

	t.Run("絶対時刻なので相対待ち時間は取れない", func(t *testing.T) {
		// 待機時刻は statusline の resets_at から取る設計なので､ここで
		// パースできないことは想定どおり｡取れてしまう方が事故になる｡
		if d, ok := ParseRelativeWait(realLimitScreen, 15); ok {
			t.Errorf("ParseRelativeWait() = (%v, true), want ok=false", d)
		}
	})

	t.Run("絶対時刻は statusline の resets_at と同じ時刻に読める", func(t *testing.T) {
		// 同じ事象の statusline は resets_at = 1786851000 を渡していた (skills/agents-daemon/references/architecture.md 参照)｡
		// 画面の 12:30pm (Asia/Tokyo) がその epoch に一致することを固定する｡
		now := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC) // 10:00 JST
		got, ok := ParseAbsoluteReset(realLimitScreen, 15, now, time.UTC)
		if !ok {
			t.Fatal("ParseAbsoluteReset() ok = false, want true")
		}
		if want := time.Unix(1786851000, 0); !got.Equal(want) {
			t.Errorf("ParseAbsoluteReset() = %v, want %v", got, want)
		}
	})
}
