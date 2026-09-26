package detect

import (
	"strings"
	"testing"
)

// compact 直後の画面。Claude Code は圧縮の境界を 1 行のヒント付きで出す
// (2.1.239 のバイナリから採取)。作業が終わっていないのにここで入力待ちになる
// ことがあり、上限表示は出ないので既存の limit 検知にはかからない。
const compactStallScreen = `⏺ 変更を適用しました。

✻ Conversation compacted (ctrl+o for history)

─────────────────────────────────────────────────────────────
❯
─────────────────────────────────────────────────────────────
  🌸 Opus 5 ･ﾟ 🧠 41k (4%) ･ﾟ ⚡ high ･ﾟ 🐣 v2.1.239
  📁 ~/src/github.com/usadamasa/claude-config ･ﾟ 🌿 main ✨
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
`

func TestCompactStallSignature(t *testing.T) {
	t.Run("compact 境界の後ろに出力が無く、空プロンプトで止まっていれば候補", func(t *testing.T) {
		if _, ok := CompactStallSignature(compactStallScreen, 15); !ok {
			t.Error("CompactStallSignature() ok = false, want true")
		}
	})

	t.Run("要約サマリ形式の境界行でも候補になる", func(t *testing.T) {
		screen := strings.Join([]string{
			"⏺ 変更を適用しました。",
			"",
			"  Compacted (ctrl+o to see full summary)",
			"",
			"─────────",
			"❯",
			"─────────",
		}, "\n")
		if _, ok := CompactStallSignature(screen, 15); !ok {
			t.Error("CompactStallSignature() ok = false, want true")
		}
	})

	t.Run("compact の後に応答が出ていれば候補でない", func(t *testing.T) {
		screen := strings.Join([]string{
			"✻ Conversation compacted (ctrl+o for history)",
			"",
			"⏺ 続きを進めます。",
			"",
			"─────────",
			"❯",
			"─────────",
		}, "\n")
		if _, ok := CompactStallSignature(screen, 15); ok {
			t.Error("CompactStallSignature() ok = true, want false (compact 後に応答がある)")
		}
	})

	t.Run("ユーザーが入力途中なら候補でない", func(t *testing.T) {
		screen := strings.Join([]string{
			"✻ Conversation compacted (ctrl+o for history)",
			"",
			"─────────",
			"❯ このあと",
			"─────────",
		}, "\n")
		if _, ok := CompactStallSignature(screen, 15); ok {
			t.Error("CompactStallSignature() ok = true, want false (入力途中)")
		}
	})

	t.Run("compact 境界が走査範囲の外なら候補でない", func(t *testing.T) {
		lines := []string{"✻ Conversation compacted (ctrl+o for history)"}
		for range 20 {
			lines = append(lines, "")
		}
		lines = append(lines, "─────────", "❯", "─────────")
		if _, ok := CompactStallSignature(strings.Join(lines, "\n"), 15); ok {
			t.Error("CompactStallSignature() ok = true, want false (境界が走査範囲外)")
		}
	})

	t.Run("compact していない通常のフッターは候補でない", func(t *testing.T) {
		screen := strings.Join([]string{
			"⏺ 完了しました。",
			"",
			"─────────",
			"❯",
			"─────────",
		}, "\n")
		if _, ok := CompactStallSignature(screen, 15); ok {
			t.Error("CompactStallSignature() ok = true, want false")
		}
	})

	t.Run("散文で compacted に触れただけの行は境界と見なさない", func(t *testing.T) {
		// README や会話がこの語に触れただけで発火すると、無関係な pane へ
		// プロンプトを打ち込む事故になる。実際の UI 行は必ず括弧付きのヒントを伴う。
		screen := strings.Join([]string{
			"⎿  この節は conversation compacted のあとの挙動を説明する",
			"",
			"─────────",
			"❯",
			"─────────",
		}, "\n")
		if _, ok := CompactStallSignature(screen, 15); ok {
			t.Error("CompactStallSignature() ok = true, want false (散文への誤反応)")
		}
	})

	t.Run("署名は入力行より下 (ステータスライン) の変化に影響されない", func(t *testing.T) {
		// ステータスラインにはコストやトークン数が並ぶ。ここを署名に含めると
		// 「画面が変わっていない」が永久に成立せず、検知が黙って死ぬ。
		other := strings.Replace(compactStallScreen, "$0", "$1", 1)
		other = strings.Replace(other, "41k (4%)", "42k (5%)", 1)

		got, ok := CompactStallSignature(compactStallScreen, 15)
		if !ok {
			t.Fatal("CompactStallSignature() ok = false, want true")
		}
		other2, ok := CompactStallSignature(other, 15)
		if !ok {
			t.Fatal("CompactStallSignature() ok = false, want true")
		}
		if got != other2 {
			t.Errorf("署名が一致しない:\n%q\n%q", got, other2)
		}
	})
}

func TestPromptIsEmpty(t *testing.T) {
	t.Run("空の入力行があれば true", func(t *testing.T) {
		if !PromptIsEmpty(compactStallScreen, 15) {
			t.Error("PromptIsEmpty() = false, want true")
		}
	})

	t.Run("入力行に書きかけがあれば false", func(t *testing.T) {
		typing := strings.Replace(compactStallScreen, "\n❯\n", "\n❯ 次はこれを\n", 1)
		if PromptIsEmpty(typing, 15) {
			t.Error("PromptIsEmpty() = true, want false")
		}
	})

	t.Run("実機の入力行 (❯ の後が U+00A0) で true", func(t *testing.T) {
		// herdr pane read で採取した実バイト。Claude Code は ❯ の後を
		// NO-BREAK SPACE で埋めるため、\s だけの正規表現では当たらない。
		real := "⏺ 直前の応答\n\n─────\n❯ \n─────\n  🌸 Sonnet 5 ･ﾟ 🧠 0k (0%)\n"
		if !PromptIsEmpty(real, 15) {
			t.Error("PromptIsEmpty() = false, want true")
		}
	})

	t.Run("入力行が末尾 n 行の外なら false", func(t *testing.T) {
		// 走査範囲より上にある空プロンプトを拾ってはいけない。
		if PromptIsEmpty(compactStallScreen, 3) {
			t.Error("PromptIsEmpty() = true, want false")
		}
	})
}
