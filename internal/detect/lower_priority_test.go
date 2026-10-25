package detect

import (
	"fmt"
	"strings"
	"testing"
)

// session limit に当たった後に `/low-priority` で低優先度の作業を続けている間の通知。2.1.310 のバイナリから採取した
// (flag `tengu_toasty_breeze`)。pane は作業中なので、上限の行が画面に残っていても待機に入ってはいけない (#251)。
var lowerPriorityActiveLines = []string{
	"Continuing now at lower priority until your limit resets at 5pm. Your weekly limit still applies, and responses may pause while waiting for spare capacity. Run /low-priority to stop.",
	"Continuing now at lower priority until your limit resets at 5:00pm. Your weekly limit still applies, and responses may pause while waiting for spare capacity. Run /low-priority to stop.",
	"Lower-priority mode is back on until your limit resets at 5pm. Your weekly limit still applies, and responses may pause while waiting for spare capacity. Run /low-priority to stop.",
	"Lower priority until 5pm · 40% allowance left · /low-priority to stop",
	"Working at lower priority · waiting for capacity",
}

// lower-priority mode が終わったことを伝える通知。ここから先は通常の上限に戻る。
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

func TestClassify_lower_priority_modeで作業中なら上限として扱わない(t *testing.T) {
	c := newTestClassifier(t)

	for i, active := range lowerPriorityActiveLines {
		t.Run(fmt.Sprintf("active-%d", i), func(t *testing.T) {
			// 上限に当たった元の行は通知の上に残っている。
			screen := strings.Join([]string{sessionLimitLine, "", active}, "\n")

			if got := c.Classify(screen, 15); got != KindNone {
				t.Errorf("Classify() = %v, want %v", got, KindNone)
			}
		})
	}
}

func TestClassify_lower_priority_modeが終わった画面は通常のlimitとして扱う(t *testing.T) {
	c := newTestClassifier(t)

	// 作業中の通知は scrollback に残ることがある。終わった通知が同じ画面にあれば、そちらを優先する。
	for i, ended := range lowerPriorityEndedLines {
		t.Run(fmt.Sprintf("ended-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{sessionLimitLine, lowerPriorityActiveLines[0], "", ended}, "\n")

			if got := c.Classify(screen, 15); got != KindLimit {
				t.Errorf("Classify() = %v, want %v", got, KindLimit)
			}
		})
	}
}

func TestClassify_lower_priority_modeの案内だけなら通常のlimitとして扱う(t *testing.T) {
	c := newTestClassifier(t)

	// 上限の画面に並ぶ案内。まだ低優先度で動いていないので、本当に上限で止まっている。
	screen := strings.Join([]string{
		sessionLimitLine,
		"/low-priority to continue now at lower priority · uses your weekly limit",
	}, "\n")

	if got := c.Classify(screen, 15); got != KindLimit {
		t.Errorf("Classify() = %v, want %v", got, KindLimit)
	}
}
