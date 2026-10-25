package mine

import (
	"regexp"
	"strings"
)

// extractApplicabilityPhrases は memory body から grep-coverage 用のフレーズを抽出する。
//
// 抽出戦略 (paraphrasing に強くする):
//  1. body を line 単位に分ける (見出し・空行は除外)
//  2. 各 line から markdown 装飾を除去
//  3. さらに日本語 punctuation ｢。 ｢｢ ｢, ｢。)｣ で分割し、sub-sentence 単位のフレーズを得る
//  4. 6 rune 未満は除外
//
// これで「前営業日 + 当日の2日分を収集する」のような自然文が
// 「前営業日 + 当日」「2日分を収集する」等の chunks に分割され、
// skill body に別表現で書かれていても部分一致しやすくなる。
func extractApplicabilityPhrases(body string) []string {
	var out []string
	seen := make(map[string]struct{})
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		cleaned := stripMarkdown(line)
		if len([]rune(cleaned)) < 6 {
			continue
		}
		// 行そのものと、sub-sentence 分割の両方を候補に入れる
		addUnique := func(s string) {
			s = strings.TrimSpace(s)
			if len([]rune(s)) < 6 {
				return
			}
			if _, ok := seen[s]; ok {
				return
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		addUnique(cleaned)
		for _, chunk := range splitJapaneseSentence(cleaned) {
			addUnique(chunk)
		}
	}
	return out
}

// splitJapaneseSentence は日本語文を句読点 (｢。 ｢, ｢｢) で分割し、sub-phrase の slice を返す。
// 括弧内の分割はしない (単純実装)。
func splitJapaneseSentence(s string) []string {
	// 分割文字: 全角/半角の句読点。 半角スペース連続も切る。
	sep := func(r rune) bool {
		switch r {
		case '。', '｡', '、', '､', ';', ':', '：', '；':
			return true
		}
		return false
	}
	fields := strings.FieldsFunc(s, sep)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

var (
	labelRe   = regexp.MustCompile(`\*\*[^*]+\*\*[::]?\s*`) // **Why:** / **How to apply:** 等
	bulletRe  = regexp.MustCompile(`^[-*]\s+`)              // 箇条書きの先頭
	prefixHdr = regexp.MustCompile(`^\d+\.\s+`)             // 番号付きリスト
)

// stripMarkdown は行内の装飾を軽く落とす (grep coverage 用)。
func stripMarkdown(line string) string {
	line = bulletRe.ReplaceAllString(line, "")
	line = prefixHdr.ReplaceAllString(line, "")
	line = labelRe.ReplaceAllString(line, "")
	// 残りの ** も装飾なので除去
	line = strings.ReplaceAll(line, "**", "")
	line = strings.TrimSpace(line)
	return line
}
