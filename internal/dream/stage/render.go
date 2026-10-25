package stage

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

// projectSlug は canonical name の先頭 - を除去した slug を返す (ファイル名/ディレクトリ名用)。
func projectSlug(project string) string {
	return strings.TrimPrefix(project, "-")
}

// reportPath は Stage 出力先を組み立てる。
// 形式: <cwd>/tmp/dream/YYYY-MM-DD-<project-slug>.md
//
// レポートはそのセッションで読んで判断するための読み捨ての文書なので、作業中のプロジェクトの
// `./tmp` に置く (利用者の作業領域で、gitignore 済みの前提)。sandbox の中で書ける場所でもある。
func reportPath(cwd, project, date string) string {
	name := fmt.Sprintf("%s-%s.md", date, projectSlug(project))
	return filepath.Join(cwd, "tmp", "dream", name)
}

// dispositionFromKind は kind から自動で決まる行き先を返す。
// unhomed は人間が決めるため空文字を返し、レポート上では空欄になる。
func dispositionFromKind(kind string) string {
	switch kind {
	case candidates.KindSkillBody:
		return "skill 本体へ統合"
	case candidates.KindMemoryDedup:
		return "memory 削除"
	}
	return ""
}

// renderReport は Stage 用の Markdown レポート本文を生成する。
// Summary + 行き先表 + 各候補セクション + 承認手順。
func renderReport(project, date string, cands []candidates.Candidate) string {
	var sb strings.Builder

	// 本文タイトル
	_, _ = fmt.Fprintf(&sb, "# dream ステージレポート (%s)\n\n", date)
	_, _ = fmt.Fprintf(&sb, "- 対象 project: `%s`\n", project)
	_, _ = fmt.Fprintf(&sb, "- 候補数: %d\n", len(cands))

	// 内訳
	skillBody, memoryDedup, unhomed := candidates.CountByKind(cands)
	_, _ = fmt.Fprintf(&sb, "- 内訳: skill_body: %d, memory_dedup: %d, unhomed: %d\n\n", skillBody, memoryDedup, unhomed)

	if len(cands) == 0 {
		sb.WriteString("この期間・project では候補は見つかりませんでした。\n")
		return sb.String()
	}

	// 行き先表: 全 memory を 1 行ずつ出す。kind から決まらない unhomed だけ空欄になるので、
	// 行き先を決めていない memory が表の上で目に見える。
	sb.WriteString("## 行き先\n\n")
	sb.WriteString("| candidate | memory | kind | 行き先 |\n|---|---|---|---|\n")
	for _, c := range cands {
		_, _ = fmt.Fprintf(&sb, "| %s | `%s` | %s | %s |\n",
			c.ID, filepath.Base(c.MemoryPath), c.Kind, dispositionFromKind(c.Kind))
	}
	sb.WriteString("\n")
	if unhomed > 0 {
		sb.WriteString("行き先が空欄の行は dream skill の「行き先テーブル」で決める。" +
			"空欄のまま `agents-daemon dream adopt` を呼ぶと拒否される。\n\n")
	}

	// 各 candidate section
	sb.WriteString("## 候補一覧\n\n")
	for _, c := range cands {
		_, _ = fmt.Fprintf(&sb, "### %s (%s / %s)\n\n", c.ID, c.Kind, c.Confidence)
		_, _ = fmt.Fprintf(&sb, "- associated_skill: `%s`\n", c.AssociatedSkill)
		_, _ = fmt.Fprintf(&sb, "- memory_path: `%s`\n", c.MemoryPath)
		if c.SkillPath != "" {
			_, _ = fmt.Fprintf(&sb, "- skill_path: `%s`\n", c.SkillPath)
		}
		_, _ = fmt.Fprintf(&sb, "- coverage_ratio: %.2f\n", c.CoverageRatio)
		if len(c.EvidenceSessions) > 0 {
			_, _ = fmt.Fprintf(&sb, "- evidence_sessions: `%s`\n", strings.Join(c.EvidenceSessions, "`, `"))
		}
		sb.WriteString("\n")
		sb.WriteString("**Diagnosis:**\n\n")
		_, _ = fmt.Fprintf(&sb, "%s\n\n", c.Diagnosis)
		sb.WriteString("**Proposed change:** Adopt phase で LLM が生成する (Stage phase では未生成)\n\n")
	}

	// 承認手順
	sb.WriteString("## 承認するには\n\n")
	sb.WriteString("各候補について、次のいずれかを判断する:\n\n")
	sb.WriteString("1. **accept as-is**: 提案どおり適用 → `agents-daemon dream adopt --candidate <id>`\n")
	sb.WriteString("2. **revise**: 修正案を指定して適用\n")
	sb.WriteString("3. **reject**: 見送り\n\n")
	sb.WriteString("Adopt は作業ブランチ `dream/YYYY-MM-DD-<slug>` で commit する。 push は user 明示指示まで保留。\n")

	return sb.String()
}
