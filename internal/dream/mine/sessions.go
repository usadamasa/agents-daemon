package mine

import (
	"sort"

	"github.com/usadamasa/agents-daemon/internal/dream/harvest/record"
)

// extractInvokedSkills は harvest sessions から skill 名を dedup + sort した slice で返す。
// Mine の第一段 (対象 skill 候補セットの決定) で使う。
func extractInvokedSkills(recs []record.Record) []string {
	set := make(map[string]struct{})
	for _, r := range recs {
		for _, s := range r.SkillsInvoked {
			set[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// projectRootFromSessions は harvest sessions の cwd から project root を推定する。
// 最初の非空 cwd を返す。見つからなければ ""。
//
// canonical name の逆演算は不可能 (- になった元の文字が / か _ か . か区別が付かない)
// なので、harvest レコードに残っている生 cwd を頼りに絶対パスを取り戻す。
func projectRootFromSessions(sessions []record.Record) string {
	for _, s := range sessions {
		if s.CWD != "" {
			return s.CWD
		}
	}
	return ""
}
