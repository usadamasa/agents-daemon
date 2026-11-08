package detect

import (
	"fmt"
	"strings"
	"testing"
)

// 上限で待っている間、Claude Code は prompt のそばの固定の通知欄に status と next を別々の行で描く (2.1.310 のバイナリ、
// #252)。next は字下げした別の Box なので必ず別の行になる。予約したときに transcript へ足される 1 行の文が走査範囲の
// 外へ出ると、画面に残るのはこの 2 行だけになる。
var footerArmedScreens = [][]string{
	{"  ⚠ Usage limit reached · limit resets 5pm", "      Continuing automatically at 5pm · esc to cancel"},
	{"  ⚠ Usage limit reached · limit resets 5:00pm", "      Continuing shortly · esc to cancel"},
	{"  ⚠ Usage limit reached", "      Continuing automatically when it resets · esc to cancel"},
	{"  ⚠ Usage limit reached · limit resets 5pm", "      Continuing automatically at 5pm · esc to cancel · /usage-credits to continue now"},
	// 記号が無い、または別の記号でも同じに扱う (記号の字は確度が低い)。
	{"  Usage limit reached · limit resets 5pm", "      Continuing automatically at 5pm · esc to cancel"},
	{"  ● Usage limit reached · limit resets 5pm", "      Continuing automatically at 5pm · esc to cancel"},
	// 狭い pane では Claude Code が status を折り返す。
	{"  ⚠ Usage limit reached · limit", "  resets 5pm", "      Continuing automatically at 5pm · esc to", "      cancel"},
}

var footerStaleScreens = [][]string{
	{"  ⚠ Your usage limit has reset", "      Press enter to continue"},
	{"  ⚠ Your usage limit has reset", "      Press enter to continue · /usage-credits to continue now"},
}

// footerScreen は上限の元の行 (transcript の上のほう) と prompt の枠の間に、通知欄の行を置いた画面を組む。
func footerScreen(footer []string) string {
	lines := []string{
		"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
		"",
		"────────────────────────────────────────",
		"❯ ",
		"────────────────────────────────────────",
	}
	return strings.Join(append(lines, footer...), "\n")
}

func TestClassifyScreen_通知欄の2行で予約中なら手を出さない(t *testing.T) {
	for i, footer := range footerArmedScreens {
		t.Run(fmt.Sprintf("armed-%d", i), func(t *testing.T) {
			if got := ClassifyScreen(footerScreen(footer), 15); got != HintNativeArmed {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNativeArmed)
			}
		})
	}
}

func TestClassifyScreen_通知欄の2行で解除済みならresume_ready(t *testing.T) {
	for i, footer := range footerStaleScreens {
		t.Run(fmt.Sprintf("stale-%d", i), func(t *testing.T) {
			if got := ClassifyScreen(footerScreen(footer), 15); got != HintResumeReady {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintResumeReady)
			}
		})
	}
}

func TestClassifyScreen_取り消しの通知が同じ画面にあれば通知欄の予約中より優先する(t *testing.T) {
	for i, standDown := range nativeStoodDownBanners {
		t.Run(fmt.Sprintf("stood-down-%d", i), func(t *testing.T) {
			screen := footerScreen(append([]string{standDown}, footerArmedScreens[0]...))

			if got := ClassifyScreen(screen, 15); got != HintNone {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNone)
			}
		})
	}
}

func TestClassifyScreen_行の途中に引用された通知欄の文は組とみなさない(t *testing.T) {
	// テストのコードや文書が画面に出ても、行頭から始まらない文は通知欄の行ではない。
	// (status と next を同じ行に引用したものは 1 行の文のパターンの担当なので、ここでは別の行に置く。)
	cases := map[string][]string{
		"armed": {
			`		"  ⚠ Usage limit reached · limit resets 5pm",`,
			`		"      Continuing automatically at 5pm · esc to cancel",`,
		},
		"stale": {
			`		"  ⚠ Your usage limit has reset",`,
			`		"      Press enter to continue",`,
		},
		"stale の status が文の一部": {
			"  Your usage limit has reset since yesterday, so",
			"  press enter to continue the review.",
		},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ClassifyScreen(strings.Join(lines, "\n"), 15); got != HintNone {
				t.Errorf("ClassifyScreen() = %v, want %v (通知欄の組でない)", got, HintNone)
			}
		})
	}
}

func TestClassifyScreen_通知欄のstatusとnextが離れていれば組とみなさない(t *testing.T) {
	screen := strings.Join([]string{
		"  ⚠ Your usage limit has reset",
		"",
		"",
		"",
		"      Press enter to continue",
	}, "\n")
	if got := ClassifyScreen(screen, 15); got != HintNone {
		t.Errorf("ClassifyScreen() = %v, want %v", got, HintNone)
	}
}
