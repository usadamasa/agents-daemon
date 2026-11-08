package detect

// 画面の通知欄の読み取りと transcript の entry の分類が、どんな入力でも落ちず、結果が決めた範囲に収まることを
// go の fuzz で確かめる。`go test` は seed だけを回し、`task fuzz` が乱択で探す。

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/transcript"
)

var fuzzScreens = []string{
	"⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)",
	"Usage limit reached · continuing automatically at 5:00pm · esc or type to cancel",
	"Usage limit has reset · press enter to continue",
	"  ⚠ Your usage limit has reset\n      Press enter to continue",
	"⚠ Session limit reached · Retrying in 3h 12m (5pm) · attempt 4/10",
	"Continuing now at lower priority until your limit resets at 5pm.",
	"\x1b[31mrate limit\x1b[0m\nresets in 2 hours",
	"",
}

func FuzzClassifyScreen(f *testing.F) {
	for _, s := range fuzzScreens {
		f.Add(s, 15)
		f.Add(s, 0)
	}
	f.Fuzz(func(t *testing.T, screen string, n int) {
		hint := ClassifyScreen(screen, n)
		if hint < HintNone || hint > HintLowerPriority {
			t.Fatalf("ClassifyScreen = %d は知らない Hint", hint)
		}
		// 末尾 n 行の上に何があっても判定は変わらない。
		if n > 0 && len(splitScreen(screen)) >= n {
			if got := ClassifyScreen("Usage limit reached · continuing automatically at 5pm · esc to cancel\n"+screen, n); got != hint {
				t.Fatalf("tail の上の行で判定が %v から %v へ変わった", hint, got)
			}
		}
	})
}

func FuzzClassifyTurn(f *testing.F) {
	f.Add("assistant", "rate_limit", "five_hour", int64(1791111000), "You've hit your session limit · resets 7:50pm (Asia/Tokyo)")
	f.Add("assistant", "rate_limit", "seven_day", int64(1787630400), "You've hit your weekly limit · resets Aug 25 at 1pm (Asia/Tokyo)")
	f.Add("assistant", "rate_limit", "", int64(0), "API Error: Request rejected (429) · This request would exceed your account's rate limit.")
	f.Add("assistant", "rate_limit", "", int64(0), "You've reached your Fable 5 limit. Run /usage-credits to continue or switch models with /model.")
	f.Add("assistant", "server_error", "", int64(0), "API Error: Your computer went to sleep mid-response.")
	f.Add("user", "", "", int64(0), "続けて")
	f.Fuzz(func(t *testing.T, typ, errKind, rateLimitType string, resetsAt int64, text string) {
		e := &transcript.Entry{Type: typ, Error: errKind, IsAPIError: errKind != "", Text: text}
		if rateLimitType != "" || resetsAt != 0 {
			e.Quota = &transcript.Quota{Status: "rejected", RateLimitType: rateLimitType}
			if resetsAt != 0 {
				e.Quota.ResetsAt = transcriptUnix(resetsAt)
			}
		}
		kind := ClassifyTurn(e)
		if kind < KindNone || kind > KindSpendLimit {
			t.Fatalf("ClassifyTurn = %d は知らない Kind", kind)
		}
		if typ != "assistant" && kind != KindNone {
			t.Fatalf("assistant でない entry を %v にした", kind)
		}
		if errKind == "" && kind != KindNone {
			t.Fatalf("error の無い entry を %v にした", kind)
		}
		if kind.Limited() && e.Error != "rate_limit" && e.Error != "overloaded" && e.Error != "server_error" {
			t.Fatalf("error %q で待機に入る %v にした", e.Error, kind)
		}
	})
}
