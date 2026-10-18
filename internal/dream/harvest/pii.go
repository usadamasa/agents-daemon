package harvest

import (
	"regexp"
)

// PII 伏せ字化ルール。
//
// 目的は「機微データが harvest 出力に生残るリスクを下げる」であって完全防御ではない。
// name-based PII (氏名) は regex では捕捉できないため、上位運用 (feedback memory /
// 個別レビュー) と組み合わせる。ここでは決定論的に検出できる ID パターンだけ扱う。
var piiPatterns = []struct {
	name    string
	pattern *regexp.Regexp
	repl    string
}{
	{
		// CADDi 面接候補者 ID (例: JP-TECH-042, US-DATA-001)
		name:    "candidate-id",
		pattern: regexp.MustCompile(`\b[A-Z]{2}-[A-Z]{2,6}-\d{2,6}\b`),
		repl:    "[CANDIDATE-ID]",
	},
	{
		// email address
		name:    "email",
		pattern: regexp.MustCompile(`\b[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}\b`),
		repl:    "[EMAIL]",
	},
	{
		// 電話番号 (日本の 090/080/070-XXXX-XXXX 系)
		name:    "phone-jp",
		pattern: regexp.MustCompile(`\b0[789]0-\d{4}-\d{4}\b`),
		repl:    "[PHONE]",
	},
}

// MaskPII は text 内の既知 PII パターンを伏せ字化する。
func MaskPII(text string) string {
	if text == "" {
		return text
	}
	out := text
	for _, p := range piiPatterns {
		out = p.pattern.ReplaceAllString(out, p.repl)
	}
	return out
}
