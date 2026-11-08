package detect

import (
	"strings"
	"testing"
)

// 2026-08-16 に実機 (Claude Code 2.1.224) で 5 時間ウィンドウの上限に到達したときの表示。上限の文は画面にあるが、
// 本体の通知欄は何も出ていない。上限かどうかは transcript が決め、画面からは「普通の再開を送る」ことしか読まない。
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

func TestClassifyScreen_通知欄の無い画面はnone(t *testing.T) {
	screens := map[string]string{
		"実機の上限の画面": realLimitScreen,
		"実機の通常のフッター": strings.Join([]string{
			`  Bash(rex capture -b "$REX_BLOCK" --trim --unwrap)`,
			``,
			`· Tinkering… (2m 52s · ↓ 9.0k tokens · thought for 8s)`,
			`  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's current work`,
			`────────────────────────────────────────────────────────────────────────────`,
			`❯`,
			`────────────────────────────────────────────────────────────────────────────`,
			`  🌸 Opus 5 💎 ･ﾟ 💰 $1.60 ･ﾟ 🧠 104k (10%) ･ﾟ ⚡ high ･ﾟ 🎀 Mikawa ･ﾟ 🐣 v2.1.224`,
		}, "\n"),
		"空": "",
		"tool result に引用された検知語": "⏺ Bash(rg -n 'rate limited' skills/)\n  ⎿  skills/x/SKILL.md:12: 上流が rate limited を返したら待つ · resets 3pm",
	}
	for name, screen := range screens {
		t.Run(name, func(t *testing.T) {
			if got := ClassifyScreen(screen, 15); got != HintNone {
				t.Errorf("ClassifyScreen() = %v, want %v", got, HintNone)
			}
		})
	}
}

func TestClassifyScreen_末尾n行だけを見る(t *testing.T) {
	screen := strings.Join(append([]string{"Usage limit has reset · press enter to continue"}, make([]string, 15)...), "\n")
	if got := ClassifyScreen(screen, 5); got != HintNone {
		t.Errorf("ClassifyScreen(n=5) = %v, want %v (走査範囲の外)", got, HintNone)
	}
	if got := ClassifyScreen(screen, 0); got != HintResumeReady {
		t.Errorf("ClassifyScreen(n=0) = %v, want %v (全行)", got, HintResumeReady)
	}
}

func TestClassifyScreen_ANSIを除いて読む(t *testing.T) {
	screen := "\x1b[33m⚠ Your usage limit has reset\x1b[0m\n\x1b[2m      Press enter to continue\x1b[0m"
	if got := ClassifyScreen(screen, 0); got != HintResumeReady {
		t.Errorf("ClassifyScreen() = %v, want %v", got, HintResumeReady)
	}
}

func TestHint_String_AllDistinct(t *testing.T) {
	seen := map[string]Hint{}
	for _, h := range []Hint{HintNone, HintNativeRetrying, HintNativeArmed, HintResumeReady, HintLowerPriority} {
		s := h.String()
		if prev, dup := seen[s]; dup {
			t.Errorf("%d と %d の String() が同じ %q", prev, h, s)
		}
		seen[s] = h
	}
}
