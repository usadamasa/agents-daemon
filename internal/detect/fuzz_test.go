package detect

// 画面の分類と時刻の読み取りが、どんな画面でも落ちず、結果が決めた範囲に収まることを go の fuzz で確かめる。
// `go test` は seed だけを回し、`task fuzz` が乱択で探す。

import (
	"testing"
	"time"
)

var fuzzSeeds = []string{
	"⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)",
	"Claude usage limit reached.\nYour limit resets at 15:00.",
	"Try again in 5 minutes",
	"⏺ API Error: 500 Internal Server Error",
	"Usage limit reached · continuing automatically at 5:00pm · esc or type to cancel",
	"Usage limit has reset · press enter to continue",
	"⏺ You've hit your monthly spend limit · Your session limit resets 5:00pm",
	"85% of your 5-hour limit · resets 3pm",
	"\x1b[31mrate limit\x1b[0m\nresets in 2 hours",
	"",
}

func FuzzClassify(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, 15)
		f.Add(s, 0)
	}
	c, err := NewClassifier(nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, screen string, n int) {
		kind := c.Classify(screen, n)
		if kind < KindNone || kind > KindSpendLimit {
			t.Fatalf("Classify = %d は知らない Kind", kind)
		}
		// 末尾 n 行の上に何があっても判定は変わらない。
		if n > 0 && len(splitScreen(screen)) >= n {
			if got := c.Classify("⏺ API Error: 529 overloaded · rate limit reached · resets 3pm\n"+screen, n); got != kind {
				t.Fatalf("tail の上の行で判定が %v から %v へ変わった", kind, got)
			}
		}
	})
}

// fuzzLocal は tz 表記の無い時刻の解釈に使う。
var fuzzLocal = time.FixedZone("Asia/Tokyo", 9*60*60)

func FuzzParseAbsoluteReset(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, 15, int64(1_787_000_000))
	}
	f.Fuzz(func(t *testing.T, screen string, n int, nowSec int64) {
		// 年が int の外へ出ない範囲に畳む。
		const span = int64(4_000_000_000)
		now := time.Unix(((nowSec%span)+span)%span, 0)
		at, ok := ParseAbsoluteReset(screen, n, now, fuzzLocal)
		if !ok {
			return
		}
		// 候補は昨日・今日・明日の同時刻で、now + maxAbsoluteResetAhead を超えない最も遅いもの。
		if at.After(now.Add(maxAbsoluteResetAhead)) || at.Before(now.Add(-26*time.Hour)) {
			t.Fatalf("ParseAbsoluteReset = %v が now %v の [-26h, +%v] の外", at, now, maxAbsoluteResetAhead)
		}
	})
}

func FuzzParseRelativeWait(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, 15)
	}
	f.Add("try again in 153722868 minutes", 0)
	f.Fuzz(func(t *testing.T, screen string, n int) {
		d, ok := ParseRelativeWait(screen, n)
		if ok && d < 0 {
			t.Fatalf("ParseRelativeWait = %v が負", d)
		}
	})
}
