package detect

import (
	"fmt"
	"strings"
	"testing"
)

// Claude Code が request の中でリトライしている間のスピナー行。unattended retry mode (`CLAUDE_CODE_RETRY_WATCHDOG`) は
// usage limit の解除もこの形で待つ。2.1.316 のバイナリのテンプレート (` · Retrying in ${_}${f} · attempt ${n}/${max}`)
// から組み立てた。上限の呼び名と時刻の書式は実画面で見ていないので、どちらにも頼らないことを確かめる。
var nativeRetryLines = []string{
	"⚠ Session limit reached · Retrying in 3h 12m (5pm) · attempt 4/10",
	"⚠ Session limit reached · Retrying in 3h 12m (5:00pm) · attempt 4/10",
	"⚠ Weekly limit reached · Retrying in 2d 4h (Nov 4, 9am) · attempt 1/10",
	"⚠ Usage limit reached · Retrying in 4m 30s · attempt 3/3",
	"⚠ API Error: 529 overloaded · Retrying in 12s · attempt 2/10",
}

const sessionLimitLine = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)"

func TestClassifyScreen_Claude_Codeがリトライ中なら手を出さない(t *testing.T) {
	for i, retry := range nativeRetryLines {
		t.Run(fmt.Sprintf("retry-%d", i), func(t *testing.T) {
			// 前の request が上限で終わった行が transcript に残っている。今の request は本体が待っているので、
			// Escape を送ると待機ごと取り消す。
			screen := strings.Join([]string{sessionLimitLine, "", retry, "", "❯ "}, "\n")

			if got := ClassifyScreen(screen, 15); got != HintNativeRetrying {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNativeRetrying)
			}
		})
	}
}

func TestClassifyScreen_リトライのスピナーが折り返されても拾う(t *testing.T) {
	// 狭い pane では attempt の手前で折り返される。
	screen := strings.Join([]string{
		sessionLimitLine,
		"",
		"⚠ Session limit reached · Retrying in 3h 12m (5pm) ·",
		"attempt 4/10",
	}, "\n")

	if got := ClassifyScreen(screen, 15); got != HintNativeRetrying {
		t.Errorf("ClassifyScreen() = %v, want %v", got, HintNativeRetrying)
	}
}

func TestClassifyScreen_attemptの無いRetrying_inはリトライ中と見なさない(t *testing.T) {
	// スピナーの形をしていない文章。
	screen := strings.Join([]string{sessionLimitLine, "", "⏺ The client logs: Retrying in 5 seconds."}, "\n")

	if got := ClassifyScreen(screen, 15); got != HintNone {
		t.Errorf("ClassifyScreen() = %v, want %v", got, HintNone)
	}
}

func TestClassifyScreen_リトライ中は通知欄の予約より優先する(t *testing.T) {
	screen := strings.Join([]string{nativeArmedBanners[0], nativeRetryLines[0]}, "\n")
	if got := ClassifyScreen(screen, 15); got != HintNativeRetrying {
		t.Errorf("ClassifyScreen() = %v, want %v", got, HintNativeRetrying)
	}
}
