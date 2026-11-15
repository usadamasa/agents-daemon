package harvest

import (
	"fmt"
	"regexp"

	"github.com/usadamasa/agents-daemon/internal/dream/policy"
)

// PII 伏せ字化ルール。
//
// 目的は「機微データが harvest 出力に生残るリスクを下げる」であって完全防御ではない。
// name-based PII (氏名) は regex では捕捉できないため、上位運用 (feedback memory /
// 個別レビュー) と組み合わせる。ここでは決定論的に検出できる ID パターンだけ扱う。
// どの利用者にも当てはまる規則だけをここに置き、利用者固有の ID の形は規約のファイル (policy) から受ける。
var builtinPII = []policy.PIIRule{
	{Name: "email", Pattern: `\b[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}\b`, Replacement: "[EMAIL]"},
	// 日本の 090/080/070-XXXX-XXXX 系
	{Name: "phone-jp", Pattern: `\b0[789]0-\d{4}-\d{4}\b`, Replacement: "[PHONE]"},
}

type piiMatcher struct {
	re   *regexp.Regexp
	repl string
}

// masker は規約の規則を先に、組み込みの規則を後に当てる。
type masker []piiMatcher

// newMasker は規約の規則と組み込みの規則を組む。規約の規則が組めなければエラー。
func newMasker(rules []policy.PIIRule) (masker, error) {
	all := append(append([]policy.PIIRule{}, rules...), builtinPII...)
	m := make(masker, 0, len(all))
	for _, r := range all {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("PII の規則 %s: %w", r.Name, err)
		}
		m = append(m, piiMatcher{re: re, repl: r.Replacement})
	}
	return m, nil
}

// mask は text 内の既知 PII パターンを伏せ字化する。
func (m masker) mask(text string) string {
	if text == "" {
		return text
	}
	out := text
	for _, p := range m {
		out = p.re.ReplaceAllLiteralString(out, p.repl)
	}
	return out
}
