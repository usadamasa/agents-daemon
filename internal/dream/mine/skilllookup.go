package mine

import (
	"fmt"
)

// loadSkillFromDirs は複数の候補ディレクトリを順に試して skill をロードする。
// 見つかった時点で終了。すべて見つからなければエラー。
//
// 用途: global skills (~/.claude/skills) と project-local skills
// (<project_root>/.claude/skills) の両方を探索する。
func loadSkillFromDirs(dirs []string, name string) (Skill, error) {
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

// projectRootFromSessions は harvest sessions の cwd から project root を推定する。
// 最初の非空 cwd を返す。見つからなければ ""。
//
// canonical name の逆演算は不可能 (- になった元の文字が / か _ か . か区別が付かない)
// なので、harvest レコードに残っている生 cwd を頼りに絶対パスを取り戻す。
func projectRootFromSessions(sessions []HarvestSession) string {
	for _, s := range sessions {
		if s.CWD != "" {
			return s.CWD
		}
	}
	return ""
}
