package detect

import (
	"strings"
	"testing"
)

// Fable のモデル単位の週次上限 (内部名 seven_day_overage_included) に当たったときの文言。2.1.316 のバイナリから採取した。
// 上限の行は「You've hit your Fable limit · resets <時刻>」で、解除が 24 時間以内なら時刻だけ、それより先なら
// 日付付きになる。ダイアログは別モデルかクレジットで続けるかを聞き、既定の選択肢はクレジットを使う側にある。
// cc-premise: 2.1.316 limit-screen Fable の上限の行とダイアログの文言
const (
	fableHitWithinDay = "⏺ You've hit your Fable limit · resets 3pm (Asia/Tokyo)"
	fableHitBeyondDay = "⏺ You've hit your Fable limit · resets Nov 7, 3pm (Asia/Tokyo)"
	fableDialogTitle  = "You've reached your Fable limit"
	fableDialogBody   = "You've used your included Fable usage for this week. Continuing on Opus 5.5 uses usage credits — you have $12.00 in credits."
	fableConsentError = "⏺ Fable limit reached · continuing on Opus 5.5 uses usage credits, and the prompt to confirm went unanswered"
)

func TestClassify_Fableの上限は待機に入らせない(t *testing.T) {
	c := newTestClassifier(t)

	// 5 時間ウィンドウの解除で起きて送ると、また同じ上限に弾かれる。ダイアログが開いていれば
	// 送った Enter がクレジットを使う選択肢を選びうる。
	cases := map[string][]string{
		"上限の行": {fableHitWithinDay},
		"上限の行とダイアログ": {
			fableHitWithinDay,
			"",
			fableDialogTitle,
			fableDialogBody,
			"❯ 1. Continue with Opus 5.5",
			"  2. Switch to Opus 5.5 and continue",
			"  3. Not now",
		},
		"同意が得られなかったエラーと session limit の解除時刻": {
			fableConsentError,
			"  Your session limit resets 5:00pm",
		},
	}

	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.Classify(strings.Join(lines, "\n"), 15); got != KindSpendLimit {
				t.Errorf("Classify() = %v, want %v", got, KindSpendLimit)
			}
		})
	}
}

func TestClassify_解除が1日より先のFableの上限は待機に入らない(t *testing.T) {
	c := newTestClassifier(t)

	if got := c.Classify(fableHitBeyondDay, 0); got == KindLimit {
		t.Errorf("Classify() = %v, want limit 以外", got)
	}
}

func TestClassify_Fableの使用率の警告はsession_limitをspend_limitにしない(t *testing.T) {
	c := newTestClassifier(t)

	// 警告は上限に当たる前に出て scrollback に残る。これで通常の上限まで待機から外すと、
	// 5 時間ウィンドウの解除で再開できなくなる。
	screen := strings.Join([]string{
		"You've used 85% of your Fable limit · resets Nov 7, 3pm (Asia/Tokyo)",
		"Approaching Fable limit",
		"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
	}, "\n")
	if got := c.Classify(screen, 0); got != KindLimit {
		t.Errorf("Classify() = %v, want %v", got, KindLimit)
	}
}
