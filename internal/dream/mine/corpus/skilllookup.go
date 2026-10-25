package corpus

import (
	"fmt"
)

// LoadSkillFromDirs は複数の候補ディレクトリを順に試して skill をロードする。
// 見つかった時点で終了。すべて見つからなければエラー。
//
// 用途: global skills (~/.claude/skills) と project-local skills
// (<project_root>/.claude/skills) の両方を探索する。
func LoadSkillFromDirs(dirs []string, name string) (Skill, error) {
	var lastErr error
	for _, d := range dirs {
		if d == "" {
			continue
		}
		sk, err := loadSkill(d, name)
		if err == nil {
			return sk, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no skill dirs provided")
	}
	return Skill{}, fmt.Errorf("skill %q not found in any dir: %w", name, lastErr)
}
