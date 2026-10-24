package mine

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/dream/dreamio"
)

// generateCandidates は memory / skill / harvest を突き合わせて candidates を生成する。
// 出力は dreamio.Candidate (mine/stage/adopt で共有される canonical schema)。
func generateCandidates(mems []memory, skills map[string]skill, project string, sessions []harvestSession) []dreamio.Candidate {
	// skill 名リスト (findAssociatedSkill 用)
	skillNames := make([]string, 0, len(skills))
	for n := range skills {
		skillNames = append(skillNames, n)
	}
	sort.Strings(skillNames)

	// skill 名 → その skill を invoke した session_id リスト
	sessionsBySkill := make(map[string][]string)
	for _, s := range sessions {
		for _, sk := range s.SkillsInvoked {
			sessionsBySkill[sk] = append(sessionsBySkill[sk], s.SessionID)
		}
	}

	var out []dreamio.Candidate
	perSkillIdx := make(map[string]int)
	for _, m := range mems {
		skillName := findAssociatedSkill(m.Name, m.Description, skillNames)
		if skillName == "" {
			// association 先が無い memory も 1 行出す。skip すると行き先を決める機会が
			// stage レポートに現れないまま memory が残り続ける。
			slug := "unhomed-" + m.Name
			perSkillIdx[slug]++
			out = append(out, dreamio.Candidate{
				ID:              buildCandidateID(slug, perSkillIdx[slug]),
				Kind:            dreamio.KindUnhomed,
				MemoryPath:      m.Path,
				ScopedToProject: project,
				Diagnosis:       buildUnhomedDiagnosis(m),
				Confidence:      dreamio.ConfidenceUnknown,
			})
			continue
		}
		loaded, ok := skills[skillName]
		if !ok {
			continue
		}

		phrases := extractApplicabilityPhrases(m.Body)
		cov := computeCoverage(phrases, loaded.Body)
		cls := classifyCoverage(cov)

		perSkillIdx[skillName]++
		cand := dreamio.Candidate{
			ID:               buildCandidateID(skillName, perSkillIdx[skillName]),
			Kind:             cls.Kind,
			AssociatedSkill:  skillName,
			MemoryPath:       m.Path,
			SkillPath:        filepath.Join(loaded.RealPath, "SKILL.md"),
			ScopedToProject:  project,
			Diagnosis:        buildDiagnosis(cls, m, cov),
			EvidenceSessions: sessionsBySkill[skillName],
			Confidence:       cls.Confidence,
			CoverageRatio:    cov.ratio(),
		}
		out = append(out, cand)
	}
	return out
}

// buildCandidateID は "dream-candidate-<skill>-NNN" 形式で ID を生成する。
func buildCandidateID(skillName string, idx int) string {
	return fmt.Sprintf("dream-candidate-%s-%03d", skillName, idx)
}

// buildDiagnosis は human-readable な診断メモを組み立てる。
func buildDiagnosis(cls classification, m memory, cov coverage) string {
	pct := int(cov.ratio() * 100)
	switch cls.Kind {
	case dreamio.KindSkillBody:
		return fmt.Sprintf("memory %q の %d/%d フレーズ (%d%%) しか skill 本体に反映されていない - migration 候補",
			m.Name, cov.Covered, cov.Total, pct)
	case dreamio.KindMemoryDedup:
		return fmt.Sprintf("memory %q は skill 本体に %d%% 反映済み - memory 側の削除候補",
			m.Name, pct)
	}
	return ""
}

// buildUnhomedDiagnosis は association 先 skill が無い memory 向けの診断メモを組み立てる。
func buildUnhomedDiagnosis(m memory) string {
	return fmt.Sprintf("memory %q に domain overlap する skill が見つからない - 行き先未定。"+
		"dream skill の行き先テーブルで行き先を決める", m.Name)
}
