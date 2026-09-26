package detect

import (
	"fmt"
	"strings"
	"testing"
)

// Claude Code 2.1.234 で入った "Continue automatically at usage limit"
// (`autoContinueAtUsageLimit`, 既定 on) が画面へ出す文言｡2.1.239 のバイナリから
// 採取した｡テンプレートは以下の 6 種で､いずれも「Claude Code 自身が解除を待って
// 続きを流す」状態を表す｡daemon はこの間､手を出してはいけない｡
//
// Escape はネイティブ側の取り消しキーでもある ("esc or type to cancel") ため､
// 待機明けに daemon が Escape を送ると継続そのものを潰してしまう｡
var nativeArmedBanners = []string{
	"Usage limit reached · continuing automatically at 5:00pm · esc or type to cancel",
	"Usage limit reached · continuing automatically shortly · esc or type to cancel",
	"Usage limit reached · continuing automatically at 5:00pm · esc to cancel",
	"Usage limit reached · continuing automatically when it resets · esc to cancel",
	"Usage limit reached · continuing shortly · esc to cancel",
	"Usage limit reached again after you continued · continuing automatically at 5:00pm · the automatic-continue setting no longer ends this wait (esc or /rate-limit-options still can)",
}

// ネイティブが降りたことを伝える文言｡ここから先は daemon だけが頼りなので､
// stand down してはいけない｡
var nativeStoodDownBanners = []string{
	"Automatic continue cancelled · /rate-limit-options to re-arm",
	"Automatic continue stopped · the usage limit now resets more than 24 hours out, so this task will not resume on its own (/rate-limit-options to wait anyway)",
	"Automatic continue was turned off · this task will not resume on its own",
	"Automatic continue did not run · the continuation was blocked before it reached the model, so this task did not resume on its own · send a prompt to continue",
	"Automatic continue stopped after repeated usage-limit hits · this task will not resume on its own (/rate-limit-options to try again)",
}

func newTestClassifier(t *testing.T) *Classifier {
	t.Helper()
	c, err := NewClassifier(nil, nil)
	if err != nil {
		t.Fatalf("NewClassifier: %v", err)
	}
	return c
}

func TestClassify_ネイティブauto_continueがカウントダウン中なら手を出さない(t *testing.T) {
	c := newTestClassifier(t)

	for i, banner := range nativeArmedBanners {
		t.Run(fmt.Sprintf("armed-%d", i), func(t *testing.T) {
			// 上限に当たった元の行はバナーの上に残っている｡二重シグナルが揃った
			// 画面でもバナーが勝つことを確かめる (勝たないと daemon が待機に入り､
			// 解除時刻に Escape を送ってネイティブの継続を取り消してしまう)｡
			screen := strings.Join([]string{
				"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
				"  /upgrade to increase your usage limit.",
				"",
				banner,
			}, "\n")

			if got := c.Classify(screen, 15); got != KindAutoContinueArmed {
				t.Errorf("Classify() = %v, want %v", got, KindAutoContinueArmed)
			}
		})
	}
}

func TestClassify_ネイティブが降りた画面は通常のlimitとして扱う(t *testing.T) {
	c := newTestClassifier(t)

	for i, banner := range nativeStoodDownBanners {
		t.Run(fmt.Sprintf("stood-down-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{
				"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
				"",
				banner,
			}, "\n")

			if got := c.Classify(screen, 15); got != KindLimit {
				t.Errorf("Classify() = %v, want %v", got, KindLimit)
			}
		})
	}
}

func TestClassify_解除済みでenter待ちならresume_ready(t *testing.T) {
	c := newTestClassifier(t)

	// ネイティブは解除まで待ったが､継続の窓を過ぎたため人間のキー入力を待つ｡
	// ここは daemon の出番だが､送るべきは Enter だけ (プロンプトを打ち込むと
	// 中断された turn ではなく新しい依頼として走ってしまう)｡
	for _, line := range []string{
		"Usage limit has reset · press enter to continue",
		"Your usage limit has reset · press enter to continue",
	} {
		t.Run(line, func(t *testing.T) {
			if got := c.Classify(line, 0); got != KindResumeReady {
				t.Errorf("Classify() = %v, want %v", got, KindResumeReady)
			}
		})
	}
}

func TestClassify_恒久的な上限は待機に入らせない(t *testing.T) {
	c := newTestClassifier(t)

	// 2.1.239: 月次 spend limit を使い切ったときの上限メッセージにも
	// session / weekly のリセット時刻が載るようになった｡二重シグナル要件を
	// 満たしてしまうが､待っても解除されない上限なので待機に入ってはいけない｡
	cases := map[string][]string{
		"monthly spend limit": {
			"⏺ You've hit your monthly spend limit · Your session limit resets 5:00pm",
			"  /usage-credits to adjust your monthly spend limit.",
		},
		"org's monthly spend limit": {
			"⏺ You've hit your org's monthly spend limit · Your weekly limit resets 5:00pm",
		},
		"usage credit limit": {
			"⏺ You've hit your usage credit limit · Your session limit resets 5:00pm",
		},
		"spend limit reached (daily)": {
			"⏺ API Error: spend limit reached (daily; resets 2026-08-08 00:00 UTC)",
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

func TestClassify_通常のsession_limitはspend_limitに巻き込まれない(t *testing.T) {
	c := newTestClassifier(t)

	screen := "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)"
	if got := c.Classify(screen, 0); got != KindLimit {
		t.Errorf("Classify() = %v, want %v", got, KindLimit)
	}
}

func TestClassify_取り消しの通知が同じ画面にあれば予約中扱いしない(t *testing.T) {
	c := newTestClassifier(t)

	// 予約中のバナーは画面下部で描き直される要素なので､取り消されても直前の
	// フレームがスクロールバックに残ることがある｡古いバナーを見て手を引き続けると､
	// ネイティブも動かずこのツールも動かない状態で固まる｡
	for i, standDown := range nativeStoodDownBanners {
		t.Run(fmt.Sprintf("stood-down-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{
				"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
				nativeArmedBanners[0],
				"",
				standDown,
			}, "\n")

			if got := c.Classify(screen, 15); got != KindLimit {
				t.Errorf("Classify() = %v, want %v", got, KindLimit)
			}
		})
	}
}
