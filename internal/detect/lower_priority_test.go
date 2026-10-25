package detect

import (
	"fmt"
	"strings"
	"testing"
)

// session limit に当たった後に `/low-priority` で低優先度の作業を続けている間の通知。2.1.310 のバイナリから採取した
// (flag `tengu_toasty_breeze`)。上限の行と並ぶと limit と判定される (#251)。挙動はまだ変えず、気付けるように
// LowerPriorityActive で印を付けるだけにする。
var lowerPriorityActiveLines = []string{
	"Continuing now at lower priority until your limit resets at 5pm. Your weekly limit still applies, and responses may pause while waiting for spare capacity. Run /low-priority to stop.",
	"Continuing now at lower priority until your limit resets at 5:00pm. Your weekly limit still applies, and responses may pause while waiting for spare capacity. Run /low-priority to stop.",
	"Lower-priority mode is back on until your limit resets at 5pm. Your weekly limit still applies, and responses may pause while waiting for spare capacity. Run /low-priority to stop.",
	"Lower priority until 5pm · 40% allowance left · /low-priority to stop",
	"Working at lower priority · waiting for capacity",
}

// lower-priority mode が終わったことを伝える通知。
var lowerPriorityEndedLines = []string{
	"Lower-priority mode ended · you have reached your weekly usage limit",
	"You've used this week's lower-priority allowance · lower-priority mode ended; it is offered again after your weekly limit resets",
	"Lower-priority mode is no longer available · it has ended; new messages wait for your usage limit to reset",
	"No room for lower-priority work for a while · lower-priority mode stopped; new messages wait for your usage limit to reset · /low-priority again after 5pm",
	"Lower-priority mode is off. New messages wait for your usage limit as usual.",
	"Lower-priority mode is taking a break until 5pm, after waiting too long for spare capacity. Try /low-priority again then.",
	"Lower-priority mode isn't available right now.",
}

const sessionLimitLine = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)"

func TestLowerPriorityActive_作業中の通知があればtrue(t *testing.T) {
	c := newTestClassifier(t)

	for i, active := range lowerPriorityActiveLines {
		t.Run(fmt.Sprintf("active-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{sessionLimitLine, "", active}, "\n")

			if !LowerPriorityActive(screen, 15) {
				t.Errorf("LowerPriorityActive() = false, want true")
			}
			// 判定はまだ変えない。上限として扱ったうえで、印だけが付く。
			if got := c.Classify(screen, 15); got != KindLimit {
				t.Errorf("Classify() = %v, want %v", got, KindLimit)
			}
		})
	}
}

func TestLowerPriorityActive_終わった通知が同じ画面にあればfalse(t *testing.T) {
	// 作業中の通知は scrollback に残ることがある。終わった通知が同じ画面にあれば、そちらを優先する。
	for i, ended := range lowerPriorityEndedLines {
		t.Run(fmt.Sprintf("ended-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{sessionLimitLine, lowerPriorityActiveLines[0], "", ended}, "\n")

			if LowerPriorityActive(screen, 15) {
				t.Errorf("LowerPriorityActive() = true, want false")
			}
		})
	}
}

func TestLowerPriorityActive_案内だけならfalse(t *testing.T) {
	// 上限の画面に並ぶ案内。まだ低優先度で動いていない。
	screen := strings.Join([]string{
		sessionLimitLine,
		"/low-priority to continue now at lower priority · uses your weekly limit",
	}, "\n")

	if LowerPriorityActive(screen, 15) {
		t.Errorf("LowerPriorityActive() = true, want false")
	}
}

func TestLowerPriorityActive_走査範囲の外の通知は見ない(t *testing.T) {
	// Classify と同じく末尾 n 行だけを見る。
	screen := strings.Join([]string{lowerPriorityActiveLines[0], "a", "b", "c"}, "\n")

	if LowerPriorityActive(screen, 3) {
		t.Errorf("LowerPriorityActive() = true, want false (範囲の外)")
	}
}
