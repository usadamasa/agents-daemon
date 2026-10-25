package mine

import (
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

// coverage は「memory の主要フレーズが skill body 内にどれだけ含まれるか」の集計。
type coverage struct {
	Covered int
	Total   int
}

// ratio は 0.0〜1.0 で coverage 率を返す。Total==0 は 0.0。
func (c coverage) ratio() float64 {
	if c.Total == 0 {
		return 0.0
	}
	return float64(c.Covered) / float64(c.Total)
}

// computeCoverage は phrases のうち skillBody に「概念として含まれる」数を数える。
//
// 判定は 3 段階 (前段が hit すれば後段は skip):
//  1. phrase 全体の substring match (完全一致)
//  2. token match: 空白・句読点・記号で分割し、3 rune 以上の token の
//     tokenMatchThreshold 以上が skillBody に含まれれば概念一致
//  3. n-gram shingle match: phrase の 4-rune shingle 集合と skill body の 4-rune
//     shingle 集合の Jaccard 類似度が shingleMatchThreshold 以上なら一致
//
// 日本語の助詞・語尾・語順違いに強い。例:
//
//	memory: "前営業日+当日の2日分を収集する"
//	skill:  "前営業日 + 当日 の2日分の生データを収集し"
//
// → literal 不一致、token 「当日の2日分を収集する」も skill 側に無いが、
//
//	shingle レベル (「前営業」「営業日」「当日の」「2日分」「収集す」等) では高一致率。
func computeCoverage(phrases []string, skillBody string) coverage {
	skillShingles := shinglesOf(skillBody, shingleSize)
	var covered int
	for _, p := range phrases {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(skillBody, p) {
			covered++
			continue
		}
		if tokenMatch(p, skillBody, tokenMatchThreshold) {
			covered++
			continue
		}
		if shingleMatch(p, skillShingles, shingleMatchThreshold) {
			covered++
		}
	}
	return coverage{Covered: covered, Total: len(phrases)}
}

const (
	tokenMatchThreshold   = 0.60 // significant token の何割以上が hit すれば概念一致
	shingleSize           = 4    // n-gram サイズ (rune 単位)
	shingleMatchThreshold = 0.40 // phrase shingle のうち何割以上が skill 側にあるか
)

// tokenMatch は phrase を token 化し、threshold 以上が skillBody に含まれるか判定する。
func tokenMatch(phrase, skillBody string, threshold float64) bool {
	tokens := tokenizeForCoverage(phrase)
	if len(tokens) == 0 {
		return false
	}
	var hits int
	for _, tok := range tokens {
		if strings.Contains(skillBody, tok) {
			hits++
		}
	}
	return float64(hits)/float64(len(tokens)) >= threshold
}

// tokenizeForCoverage は phrase を「significant token」の slice に分割する。
// 分割区切り: 空白・ASCII 句読点・全角句読点・記号 (+, =, /, →, 数記号など)。
// 3 rune 未満の token は helper particle として除外。
func tokenizeForCoverage(phrase string) []string {
	sep := func(r rune) bool {
		if r == ' ' || r == '\t' || r == '\n' {
			return true
		}
		switch r {
		case '+', '=', '/', ',', '.', ':', ';', '(', ')', '（', '）',
			'。', '｡', '、', '､', '：', '；', '「', '」', '『', '』',
			'→', '←', '↓', '↑', '-', '~', '〜', '｜', '|', '_':
			return true
		}
		return false
	}
	fields := strings.FieldsFunc(phrase, sep)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if len([]rune(f)) < 3 {
			continue
		}
		out = append(out, f)
	}
	return out
}

// shinglesOf は text の n-rune 連続部分文字列 (n-gram) 集合を返す。
// map の存在確認だけに使うので value は struct{}。
func shinglesOf(text string, n int) map[string]struct{} {
	runes := []rune(text)
	if len(runes) < n {
		return nil
	}
	out := make(map[string]struct{}, len(runes)-n+1)
	for i := 0; i+n <= len(runes); i++ {
		out[string(runes[i:i+n])] = struct{}{}
	}
	return out
}

// shingleMatch は phrase の shingle のうち threshold 以上が skillShingles に含まれるか判定する。
func shingleMatch(phrase string, skillShingles map[string]struct{}, threshold float64) bool {
	phraseShingles := shinglesOf(phrase, shingleSize)
	if len(phraseShingles) == 0 || len(skillShingles) == 0 {
		return false
	}
	var hits int
	for s := range phraseShingles {
		if _, ok := skillShingles[s]; ok {
			hits++
		}
	}
	return float64(hits)/float64(len(phraseShingles)) >= threshold
}

// findAssociatedSkill は memory 名 / description から関連 skill 名を推定する。
// skill 名が独立した語として現れていればマッチとする。見つからなければ ""。
func findAssociatedSkill(memName, memDescription string, candidateSkills []string) string {
	// より長い skill 名から順に match させる (specific 優先)
	// e.g., "daily-standup" は "standup" より先に評価する
	longest := ""
	for _, s := range candidateSkills {
		if s == "" {
			continue
		}
		if containsAsToken(memName, s) || containsAsToken(memDescription, s) {
			if len(s) > len(longest) {
				longest = s
			}
		}
	}
	if longest != "" {
		return longest
	}
	// より短い substring key (skill 名の一部) でもう一度試す (e.g., "standup" → "daily-standup")
	// これは memory 命名規則が feedback-<topic>-<detail> であることを前提とする。
	for _, s := range candidateSkills {
		key := extractSkillKey(s)
		if key == "" {
			continue
		}
		if containsAsToken(memName, key) || containsAsToken(memDescription, key) {
			return s
		}
	}
	return ""
}

// containsAsToken は needle が haystack 内に独立した語として現れるかを判定する。
//
// 素の substring match だと短い skill 名が無関係な語の内部に埋もれて誤爆する
// (skill "mo" が "mock" / "move" / "remove" に hit する)。前後の 1 byte が
// ASCII 英数字なら語の一部とみなして退ける。
//
// ハイフン (kebab-case の区切り) と非 ASCII (日本語) は語境界として扱う。
// UTF-8 の継続 byte は必ず 0x80 以上なので byte 単位の判定で安全。
func containsAsToken(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for off := 0; off+len(needle) <= len(haystack); {
		i := strings.Index(haystack[off:], needle)
		if i < 0 {
			return false
		}
		start := off + i
		if !isASCIIWordByte(haystack, start-1) && !isASCIIWordByte(haystack, start+len(needle)) {
			return true
		}
		off = start + 1
	}
	return false
}

// isASCIIWordByte は s[i] が ASCII 英数字かを返す。範囲外は false (文字列端は語境界)。
func isASCIIWordByte(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// extractSkillKey は "daily-standup" → "standup" のような検索キーを返す。
// prefix 部分 (daily- / caddi- 等) が memory 名から省略される慣習に対応する。
func extractSkillKey(name string) string {
	// ハイフンで分割し、最後の 2 tokens を key とする (例: "daily-standup" → "standup")
	// 単一 token skill (例: "dream") はそのまま
	parts := strings.Split(name, "-")
	if len(parts) <= 1 {
		return name
	}
	// prefix が "caddi" "daily" "weekly" などなら末尾 tokens を返す
	knownPrefixes := map[string]bool{"caddi": true, "daily": true, "weekly": true}
	if knownPrefixes[parts[0]] {
		return strings.Join(parts[1:], "-")
	}
	return name
}

// classification は Mine 結果 candidate 1 件の分類 (kind + confidence)。
// 値は candidates の Kind* / Confidence* 定数を使う。
type classification struct {
	Kind       string
	Confidence string
}

// classifyCoverage は coverage ratio に基づいて candidate の kind と confidence を決める。
// 閾値は skills/dream/references/skill-mining-heuristics.md の「判定 4」表を
// single source of truth とする。数値を変更したら reference 側も同時に更新すること。
func classifyCoverage(c coverage) classification {
	// Total==0 は「フレーズ抽出できなかった memory」で判定不能 → skill_body low
	if c.Total == 0 {
		return classification{Kind: candidates.KindSkillBody, Confidence: candidates.ConfidenceLow}
	}
	ratio := c.ratio()
	switch {
	case ratio >= 0.95:
		// ほぼ完全 inline → 削除して安全
		return classification{Kind: candidates.KindMemoryDedup, Confidence: candidates.ConfidenceHigh}
	case ratio >= 0.80:
		// 概ね inline だが一部残 → 削除候補、要確認
		return classification{Kind: candidates.KindMemoryDedup, Confidence: candidates.ConfidenceMedium}
	case ratio <= 0.15:
		// ほぼ inline されていない → migration 候補、確度高い
		return classification{Kind: candidates.KindSkillBody, Confidence: candidates.ConfidenceHigh}
	case ratio <= 0.40:
		// 部分的にしか inline されていない → migration 候補、middle confidence
		return classification{Kind: candidates.KindSkillBody, Confidence: candidates.ConfidenceMedium}
	default:
		// 40-80% は判定困難 → skill_body low とし人間判断に任せる
		return classification{Kind: candidates.KindSkillBody, Confidence: candidates.ConfidenceLow}
	}
}
