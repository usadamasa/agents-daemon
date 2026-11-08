package detect

import (
	"fmt"
	"strings"
	"testing"
)

// Claude Code 2.1.234 で入った "Continue automatically at usage limit"
// (`autoContinueAtUsageLimit`, 既定 on) が画面へ出す文言。2.1.239 のバイナリから
// 採取した。テンプレートは以下の 6 種で、いずれも「Claude Code 自身が解除を待って
// 続きを流す」状態を表す。daemon はこの間、手を出してはいけない。
//
// Escape はネイティブ側の取り消しキーでもある ("esc or type to cancel") ため、
// 待機明けに daemon が Escape を送ると継続そのものを潰してしまう。
var nativeArmedBanners = []string{
	"Usage limit reached · continuing automatically at 5:00pm · esc or type to cancel",
	"Usage limit reached · continuing automatically shortly · esc or type to cancel",
	"Usage limit reached · continuing automatically at 5:00pm · esc to cancel",
	"Usage limit reached · continuing automatically when it resets · esc to cancel",
	"Usage limit reached · continuing shortly · esc to cancel",
	"Usage limit reached again after you continued · continuing automatically at 5:00pm · the automatic-continue setting no longer ends this wait (esc or /rate-limit-options still can)",
}

// ネイティブが降りたことを伝える文言。ここから先は daemon だけが頼りなので、
// stand down してはいけない。
var nativeStoodDownBanners = []string{
	"Automatic continue cancelled · /rate-limit-options to re-arm",
	"Automatic continue stopped · the usage limit now resets more than 24 hours out, so this task will not resume on its own (/rate-limit-options to wait anyway)",
	"Automatic continue was turned off · this task will not resume on its own",
	"Automatic continue did not run · the continuation was blocked before it reached the model, so this task did not resume on its own · send a prompt to continue",
	"Automatic continue stopped after repeated usage-limit hits · this task will not resume on its own (/rate-limit-options to try again)",
}

func TestClassifyScreen_ネイティブauto_continueがカウントダウン中なら手を出さない(t *testing.T) {
	for i, banner := range nativeArmedBanners {
		t.Run(fmt.Sprintf("armed-%d", i), func(t *testing.T) {
			// 上限に当たった元の行はバナーの上に残っている。
			screen := strings.Join([]string{
				"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
				"  /upgrade to increase your usage limit.",
				"",
				banner,
			}, "\n")

			if got := ClassifyScreen(screen, 15); got != HintNativeArmed {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNativeArmed)
			}
		})
	}
}

func TestClassifyScreen_ネイティブが降りた画面は通常の再開を送る(t *testing.T) {
	for i, banner := range nativeStoodDownBanners {
		t.Run(fmt.Sprintf("stood-down-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{
				"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
				"",
				banner,
			}, "\n")

			if got := ClassifyScreen(screen, 15); got != HintNone {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNone)
			}
		})
	}
}

func TestClassifyScreen_解除済みでenter待ちならresume_ready(t *testing.T) {
	// ネイティブは解除まで待ったが、継続の窓を過ぎたため人間のキー入力を待つ。
	// ここは daemon の出番だが、送るべきは Enter だけ (プロンプトを打ち込むと
	// 中断された turn ではなく新しい依頼として走ってしまう)。
	for _, line := range []string{
		"Usage limit has reset · press enter to continue",
		"Your usage limit has reset · press enter to continue",
	} {
		t.Run(line, func(t *testing.T) {
			if got := ClassifyScreen(line, 0); got != HintResumeReady {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintResumeReady)
			}
		})
	}
}

func TestClassifyScreen_取り消しの通知が同じ画面にあれば予約中扱いしない(t *testing.T) {
	// 予約中のバナーは画面下部で描き直される要素なので、取り消されても直前の
	// フレームがスクロールバックに残ることがある。古いバナーを見て手を引き続けると、
	// ネイティブも動かずこのツールも動かない状態で固まる。
	for i, standDown := range nativeStoodDownBanners {
		t.Run(fmt.Sprintf("stood-down-%d", i), func(t *testing.T) {
			screen := strings.Join([]string{
				"⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)",
				nativeArmedBanners[0],
				"",
				standDown,
			}, "\n")

			if got := ClassifyScreen(screen, 15); got != HintNone {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNone)
			}
		})
	}
}
