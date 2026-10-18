package harvest

import (
	"regexp"
	"strings"
)

// feedback パターン。日本語・英語 mixed。word boundary は日本語で機能しないので
// 部分一致を許容し、上位でノイズを吸収する。
type feedbackPattern struct {
	kind    string
	pattern *regexp.Regexp
}

// 英語 pattern は \b で単語境界を強制し、"rejected" "rejecting" 等の派生形に誤反応しないようにする。
// 日本語 pattern には \b が効かないため、skill 本体の混入は isMetaUserContent (harvest.go) 側で
// 除外する。issue #173 参照。
var feedbackPatterns = []feedbackPattern{
	{
		kind:    "correction",
		pattern: regexp.MustCompile(`(?i)そうじゃない|そうじゃなくて|違うよ|違います|ちがう|\bno,\s*that's\s*wrong\b|\bnot\s*that\b|\bthat's\s*wrong\b`),
	},
	{
		kind:    "stop_doing",
		pattern: regexp.MustCompile(`(?i)やめて|やめろ|\bstop\s+doing\b|\bstop\s+that\b|\bdon'?t\s+do\s+that\b`),
	},
	{
		kind:    "rejection",
		pattern: regexp.MustCompile(`(?i)却下|使わないで|使うな|\breject\b|\bdo\s*not\s+use\b`),
	},
}

// detectFeedback は text が feedback signal かを判定し、該当すれば FeedbackSignal を返す。
// 複数パターン該当時は最初にマッチした kind を採用する (correction を優先させる並び順)。
func detectFeedback(text string) (FeedbackSignal, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return FeedbackSignal{}, false
	}
	for _, fp := range feedbackPatterns {
		if fp.pattern.MatchString(trimmed) {
			return FeedbackSignal{
				Quote: truncate(MaskPII(trimmed), maxFeedbackQuote),
				Kind:  fp.kind,
			}, true
		}
	}
	return FeedbackSignal{}, false
}
