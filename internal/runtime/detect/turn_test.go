package detect

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// 本文は 2026 年に実機で踏んだ上限の transcript の entry から写した (transcript/testdata)。
const (
	sessionLimitText   = "You've hit your session limit · resets 7:50pm (Asia/Tokyo)"
	weeklyLimitText    = "You've hit your weekly limit · resets Aug 25 at 1pm (Asia/Tokyo) · progress saved"
	requestRejected429 = "API Error: Request rejected (429) · This request would exceed your account's rate limit. Please try again later."
	fableLimitText     = "You've reached your Fable 5 limit. Run /usage-credits to continue or switch models with /model."
	sleepServerError   = "API Error: Your computer went to sleep mid-response. The response above may be incomplete."
)

var resetsAt = time.Unix(1791111000, 0)

func rateLimited(rateLimitType, text string) *transcript.Entry {
	return &transcript.Entry{
		Type: "assistant", Error: "rate_limit", IsAPIError: true, APIErrorStatus: 429, Text: text,
		Quota: &transcript.Quota{Status: "rejected", RateLimitType: rateLimitType, ResetsAt: resetsAt, OverageStatus: "rejected"},
	}
}

func apiError(kind, text string) *transcript.Entry {
	return &transcript.Entry{Type: "assistant", Error: kind, IsAPIError: true, Text: text}
}

func TestClassifyTurn(t *testing.T) {
	tests := []struct {
		name  string
		entry *transcript.Entry
		want  Kind
	}{
		{"entry が無い", nil, KindNone},
		{"利用者の prompt が最後 (再開した)", &transcript.Entry{Type: "user", Text: "続けて"}, KindNone},
		{"assistant の普通の応答", &transcript.Entry{Type: "assistant", Text: "done"}, KindNone},
		{"tool_result の user の entry (turn の途中)", &transcript.Entry{Type: "user"}, KindNone},
		// quotaLimits のある rate_limit (2.1.234 以降)。
		{"five_hour", rateLimited("five_hour", sessionLimitText), KindLimit},
		{"seven_day", rateLimited("seven_day", weeklyLimitText), KindWeeklyLimit},
		{"seven_day_opus", rateLimited("seven_day_opus", weeklyLimitText), KindWeeklyLimit},
		{"seven_day_overage_included", rateLimited("seven_day_overage_included", weeklyLimitText), KindWeeklyLimit},
		{"知らない rateLimitType でも resetsAt があれば limit として待つ", rateLimited("something_new", sessionLimitText), KindLimit},
		// quotaLimits の無い rate_limit は本文で分ける。
		{"アカウント全体の rate limit (429 の Request rejected) は transient", apiError("rate_limit", requestRejected429), KindTransient},
		{"Fable のモデル単位の上限は hard cap", apiError("rate_limit", fableLimitText), KindSpendLimit},
		{"spend limit は hard cap", apiError("rate_limit", "You've hit your monthly spend limit · Your session limit resets 5:00pm"), KindSpendLimit},
		{"usage credit limit は hard cap", apiError("rate_limit", "You've hit your usage credit limit · Your session limit resets 5:00pm"), KindSpendLimit},
		{"quotaLimits も無く本文が上限の文 (古い版) は limit", apiError("rate_limit", "You've hit your session limit · resets 5:50pm (Asia/Tokyo)"), KindLimit},
		{"resetsAt の無い quotaLimits は無いものとして本文で分ける", &transcript.Entry{Type: "assistant", Error: "rate_limit", IsAPIError: true, Text: requestRejected429, Quota: &transcript.Quota{Status: "rejected"}}, KindTransient},
		// rate_limit 以外の error。
		{"server_error は transient", apiError("server_error", sleepServerError), KindTransient},
		{"overloaded は transient", apiError("overloaded", "API Error: 529 Overloaded"), KindTransient},
		{"invalid_request は daemon が送って直らない", apiError("invalid_request", "API Error: 400 due to tool use concurrency issues."), KindNone},
		{"billing_error は daemon が送って直らない", apiError("billing_error", "API Error: 402 credit balance is too low"), KindNone},
		{"unknown は daemon が送って直らない", apiError("unknown", "API Error: Request was aborted."), KindNone},
		{"user の entry に error が付いていても見ない", &transcript.Entry{Type: "user", Error: "rate_limit"}, KindNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyTurn(tt.entry); got != tt.want {
				t.Errorf("ClassifyTurn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKind_Limited(t *testing.T) {
	for k, want := range map[Kind]bool{KindNone: false, KindLimit: true, KindWeeklyLimit: true, KindTransient: true, KindSpendLimit: false} {
		if got := k.Limited(); got != want {
			t.Errorf("%v.Limited() = %v, want %v", k, got, want)
		}
	}
}

func TestKind_String_AllDistinct(t *testing.T) {
	seen := map[string]Kind{}
	for _, k := range []Kind{KindNone, KindLimit, KindWeeklyLimit, KindTransient, KindSpendLimit} {
		s := k.String()
		if prev, dup := seen[s]; dup {
			t.Errorf("%d と %d の String() が同じ %q", prev, k, s)
		}
		seen[s] = k
	}
}
